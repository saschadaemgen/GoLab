package handler

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

// Sprint 16b Phase 2: project + doc authoring pages. The eight
// handlers below add server-rendered forms on top of the read-only
// pages from project_pages.go and the API endpoints from project.go.
//
// Pattern: GET handlers render a form template with a Form view-model
// pre-filled from either the user's last submission (validation
// failure) or the existing record (edit). POST handlers parse the
// form-encoded body, validate, mutate via the store, then redirect
// (PRG) on success or re-render the form template with .Error +
// preserved Form values on validation failure. Server-side errors
// (500-class) write the standard error page; the user keeps their
// input via the back button.

// projectFormValues is what the project-new / project-edit templates
// read from .Content.Form. Mirrors the shape ValidateProjectSlug + the
// ProjectStore expect after parsing.
type projectFormValues struct {
	Name        string
	Slug        string
	Description string
	Status      string
	Visibility  string
	Icon        string
	Color       string
}

func defaultProjectFormValues() projectFormValues {
	return projectFormValues{
		Status:     model.ProjectStatusDraft,
		Visibility: model.ProjectVisibilityPublic,
	}
}

func formValuesFromProject(p *model.Project) projectFormValues {
	return projectFormValues{
		Name:        p.Name,
		Slug:        p.Slug,
		Description: p.Description,
		Status:      p.Status,
		Visibility:  p.Visibility,
		Icon:        p.Icon,
		Color:       p.Color,
	}
}

func formValuesFromRequest(r *http.Request) projectFormValues {
	return projectFormValues{
		Name:        strings.TrimSpace(r.PostFormValue("name")),
		Slug:        strings.TrimSpace(strings.ToLower(r.PostFormValue("slug"))),
		Description: r.PostFormValue("description"),
		Status:      strings.TrimSpace(r.PostFormValue("status")),
		Visibility:  strings.TrimSpace(r.PostFormValue("visibility")),
		Icon:        strings.TrimSpace(r.PostFormValue("icon")),
		Color:       strings.TrimSpace(r.PostFormValue("color")),
	}
}

// ============================================================
// Project create
// ============================================================

// NewProjectPage renders the empty create form. Owner-only
// (power_level >= 100). Sprint 20 will replace the hard threshold
// with a TL-based, site-settings-configurable gate.
func (h *ProjectHandler) NewProjectPage(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil || user.PowerLevel < 100 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	space, ok := h.loadSpaceForPage(w, r)
	if !ok {
		return
	}
	h.renderProjectNew(w, r, space, defaultProjectFormValues(), "")
}

// CreateProjectFromForm handles the POST that follows NewProjectPage.
// On success it redirects to the project detail page. On validation
// failure it re-renders the form with the user's values preserved.
func (h *ProjectHandler) CreateProjectFromForm(w http.ResponseWriter, r *http.Request) {
	space, ok := h.loadSpaceForPage(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil || user.PowerLevel < 100 {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderProjectNew(w, r, space, defaultProjectFormValues(), "Could not parse form")
		return
	}

	form := formValuesFromRequest(r)
	if form.Name == "" || len(form.Name) > maxProjectNameLen {
		h.renderProjectNew(w, r, space, form, "Name must be 1-80 characters")
		return
	}
	if len(form.Description) > maxProjectDescLen {
		h.renderProjectNew(w, r, space, form, "Description is too long (max 500 characters)")
		return
	}
	if form.Slug == "" {
		form.Slug = model.SlugifyProject(form.Name)
	}
	if err := model.ValidateProjectSlug(form.Slug); err != nil {
		h.renderProjectNew(w, r, space, form, "Slug must be 3-64 lowercase letters, digits, or hyphens")
		return
	}
	if form.Status == "" {
		form.Status = model.ProjectStatusDraft
	}
	if !validProjectStatus(form.Status) {
		h.renderProjectNew(w, r, space, form, "Invalid status")
		return
	}
	if form.Visibility == "" {
		form.Visibility = model.ProjectVisibilityPublic
	}
	if !validProjectVisibility(form.Visibility) {
		h.renderProjectNew(w, r, space, form, "Invalid visibility")
		return
	}
	if len(form.Icon) > maxProjectIconLen {
		h.renderProjectNew(w, r, space, form, "Icon is too long (max 16 characters)")
		return
	}
	if len(form.Color) > maxProjectColorLen {
		h.renderProjectNew(w, r, space, form, "Color is too long (max 16 characters)")
		return
	}

	project, err := h.Projects.Create(r.Context(), model.ProjectCreateParams{
		SpaceID:     space.ID,
		Slug:        form.Slug,
		Name:        form.Name,
		Description: form.Description,
		Status:      form.Status,
		Visibility:  form.Visibility,
		OwnerID:     user.ID,
		Icon:        form.Icon,
		Color:       form.Color,
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrProjectInvalidSlug):
			h.renderProjectNew(w, r, space, form, "Slug must be 3-64 lowercase letters, digits, or hyphens")
		case errors.Is(err, model.ErrProjectSlugTaken):
			h.renderProjectNew(w, r, space, form, "A project with this slug already exists in this space")
		default:
			slog.Error("create project from form", "error", err)
			h.renderProjectNew(w, r, space, form, "Internal error - please try again")
		}
		return
	}

	slog.Info("project created via form",
		"id", project.ID, "slug", project.Slug,
		"space", space.Slug, "owner", user.Username)

	http.Redirect(w, r,
		"/spaces/"+space.Slug+"/projects/"+project.Slug,
		http.StatusSeeOther)
}

func (h *ProjectHandler) renderProjectNew(w http.ResponseWriter, r *http.Request, space *model.Space, form projectFormValues, errMsg string) {
	data := h.newProjectPageData(r, "New project - "+space.Name, space)
	data["Content"] = map[string]any{
		"Space": space,
		"Form":  form,
		"Error": errMsg,
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := h.Render.Render(w, "project-new", data); err != nil {
		slog.Error("render project-new", "error", err)
	}
}

// ============================================================
// Project edit
// ============================================================

// EditProjectPage renders the edit form pre-filled from the existing
// project. Owner-or-admin only.
func (h *ProjectHandler) EditProjectPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.canUserManageProject(user, project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	h.renderProjectEdit(w, r, space, project, formValuesFromProject(project), "")
}

// UpdateProjectFromForm handles the POST that follows EditProjectPage.
func (h *ProjectHandler) UpdateProjectFromForm(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.canUserManageProject(user, project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderProjectEdit(w, r, space, project, formValuesFromProject(project), "Could not parse form")
		return
	}

	form := formValuesFromRequest(r)
	form.Slug = project.Slug // immutable on edit; ignore any submitted slug
	if form.Name == "" || len(form.Name) > maxProjectNameLen {
		h.renderProjectEdit(w, r, space, project, form, "Name must be 1-80 characters")
		return
	}
	if len(form.Description) > maxProjectDescLen {
		h.renderProjectEdit(w, r, space, project, form, "Description is too long (max 500 characters)")
		return
	}
	if !validProjectStatus(form.Status) {
		h.renderProjectEdit(w, r, space, project, form, "Invalid status")
		return
	}
	if !validProjectVisibility(form.Visibility) {
		h.renderProjectEdit(w, r, space, project, form, "Invalid visibility")
		return
	}
	if len(form.Icon) > maxProjectIconLen {
		h.renderProjectEdit(w, r, space, project, form, "Icon is too long (max 16 characters)")
		return
	}
	if len(form.Color) > maxProjectColorLen {
		h.renderProjectEdit(w, r, space, project, form, "Color is too long (max 16 characters)")
		return
	}

	if err := h.Projects.Update(r.Context(), model.ProjectUpdateParams{
		ID:          project.ID,
		Name:        form.Name,
		Description: form.Description,
		Status:      form.Status,
		Visibility:  form.Visibility,
		Icon:        form.Icon,
		Color:       form.Color,
	}); err != nil {
		if errors.Is(err, model.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("update project from form", "id", project.ID, "error", err)
		h.renderProjectEdit(w, r, space, project, form, "Internal error - please try again")
		return
	}

	slog.Info("project updated via form",
		"id", project.ID, "slug", project.Slug, "actor", user.Username)

	http.Redirect(w, r,
		"/spaces/"+space.Slug+"/projects/"+project.Slug,
		http.StatusSeeOther)
}

func (h *ProjectHandler) renderProjectEdit(w http.ResponseWriter, r *http.Request, space *model.Space, project *model.Project, form projectFormValues, errMsg string) {
	data := h.newProjectPageData(r, "Edit "+project.Name, space)
	data["Content"] = map[string]any{
		"Space":   space,
		"Project": project,
		"Form":    form,
		"Error":   errMsg,
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := h.Render.Render(w, "project-edit", data); err != nil {
		slog.Error("render project-edit", "error", err)
	}
}

// ============================================================
// Project delete (soft)
// ============================================================

// DeleteProjectFromForm handles POST .../delete from the edit page's
// danger zone. Soft-deletes via the store (SoftDelete sets deleted_at)
// and redirects to the project list.
func (h *ProjectHandler) DeleteProjectFromForm(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.canUserManageProject(user, project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if err := h.Projects.SoftDelete(r.Context(), project.ID); err != nil {
		if errors.Is(err, model.ErrProjectNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("soft-delete project from form", "id", project.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("project soft-deleted via form",
		"id", project.ID, "slug", project.Slug, "actor", user.Username)
	http.Redirect(w, r, "/spaces/"+space.Slug+"/projects", http.StatusSeeOther)
}

// ============================================================
// Doc editor (canonical types)
// ============================================================

// EditDocPage renders the Quill editor for the four canonical doc
// types. If a doc already exists, its current ContentHTML seeds the
// editor; otherwise the editor opens empty. Owner / contributor /
// admin only.
func (h *ProjectHandler) EditDocPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil || !(h.canUserManageProject(user, project) ||
		h.canUserEditProject(r, user, project)) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	docType := chi.URLParam(r, "doc_type")
	if !model.IsValidProjectDocType(docType) || docType == model.ProjectDocCustom {
		http.NotFound(w, r)
		return
	}

	var doc *model.ProjectDoc
	if d, err := h.ProjectDocs.GetByType(r.Context(), project.ID, docType); err == nil {
		doc = d
	} else if !errors.Is(err, model.ErrProjectDocNotFound) {
		slog.Error("edit doc page: load", "project", project.ID, "type", docType, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderDocEdit(w, r, space, project, docType, doc, "")
}

// SaveDocFromForm processes the form POST from the Quill editor.
// content_html is the raw HTML from Quill; we sanitize it before
// storage. content_md is left empty for canonical / Quill-authored
// docs (Sprint 16b decision: Quill HTML is canonical, content_md is
// reserved for the API path which still accepts Markdown).
func (h *ProjectHandler) SaveDocFromForm(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if user == nil || !(h.canUserManageProject(user, project) ||
		h.canUserEditProject(r, user, project)) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	docType := chi.URLParam(r, "doc_type")
	if !model.IsValidProjectDocType(docType) {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "could not parse form", http.StatusBadRequest)
		return
	}

	rawHTML := r.PostFormValue("content_html")
	if len(rawHTML) > maxDocContentLen {
		var existing *model.ProjectDoc
		if d, _ := h.ProjectDocs.GetByType(r.Context(), project.ID, docType); d != nil {
			existing = d
		}
		h.renderDocEdit(w, r, space, project, docType, existing, "Document is too long (max 100,000 characters)")
		return
	}

	title := strings.TrimSpace(r.PostFormValue("title"))
	if docType == model.ProjectDocCustom {
		if title == "" || len(title) > maxDocTitleLen {
			var existing *model.ProjectDoc
			if d, _ := h.ProjectDocs.GetByType(r.Context(), project.ID, docType); d != nil {
				existing = d
			}
			h.renderDocEdit(w, r, space, project, docType, existing, "Custom doc title must be 1-120 characters")
			return
		}
	} else {
		// Canonical types ignore the submitted title; the label is
		// fixed (Concept / Architecture / Workflow / Roadmap) and the
		// Title column gets the label so list views show something
		// meaningful even before content lands.
		title = docTypeLabel(docType)
	}

	sortOrder := 0
	if v := r.PostFormValue("sort_order"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 999 {
			sortOrder = n
		}
	}

	contentHTML := ""
	if rawHTML != "" {
		if h.Sanitizer != nil {
			contentHTML = h.Sanitizer.Clean(rawHTML)
		} else {
			contentHTML = rawHTML
		}
	}

	if _, err := h.ProjectDocs.Upsert(r.Context(), model.ProjectDocUpsertParams{
		ProjectID:   project.ID,
		DocType:     docType,
		Title:       title,
		ContentMD:   "",
		ContentHTML: contentHTML,
		SortOrder:   sortOrder,
		EditedBy:    user.ID,
	}); err != nil {
		slog.Error("save doc from form", "project", project.ID, "type", docType, "error", err)
		var existing *model.ProjectDoc
		if d, _ := h.ProjectDocs.GetByType(r.Context(), project.ID, docType); d != nil {
			existing = d
		}
		h.renderDocEdit(w, r, space, project, docType, existing, "Internal error - please try again")
		return
	}

	slog.Info("project doc saved via form",
		"project", project.ID, "type", docType, "actor", user.Username)

	http.Redirect(w, r,
		"/spaces/"+space.Slug+"/projects/"+project.Slug+"/docs/"+docType,
		http.StatusSeeOther)
}

func (h *ProjectHandler) renderDocEdit(w http.ResponseWriter, r *http.Request, space *model.Space, project *model.Project, docType string, doc *model.ProjectDoc, errMsg string) {
	data := h.newProjectPageData(r, "Edit "+docTypeLabel(docType)+" - "+project.Name, space)
	data["Content"] = map[string]any{
		"Space":    space,
		"Project":  project,
		"Doc":      doc,
		"DocType":  docType,
		"DocLabel": docTypeLabel(docType),
		"Error":    errMsg,
	}
	if errMsg != "" {
		w.WriteHeader(http.StatusBadRequest)
	}
	if err := h.Render.Render(w, "project-doc-edit", data); err != nil {
		slog.Error("render project-doc-edit", "error", err)
	}
}

// ============================================================
// Doc delete (custom only)
// ============================================================

// DeleteDocFromForm processes the danger-zone form on the doc editor
// page for custom docs. Canonical types are not deletable - their
// slot is permanent; users can edit content to empty if they want.
func (h *ProjectHandler) DeleteDocFromForm(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.canUserManageProject(user, project) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	docID, err := strconv.ParseInt(chi.URLParam(r, "doc_id"), 10, 64)
	if err != nil || docID <= 0 {
		http.NotFound(w, r)
		return
	}
	doc, err := h.ProjectDocs.GetByID(r.Context(), docID)
	if err != nil {
		if errors.Is(err, model.ErrProjectDocNotFound) {
			http.NotFound(w, r)
			return
		}
		slog.Error("delete doc from form: load", "id", docID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if doc.ProjectID != project.ID {
		http.NotFound(w, r)
		return
	}
	if doc.DocType != model.ProjectDocCustom {
		http.Error(w, "only custom docs can be deleted", http.StatusBadRequest)
		return
	}
	if err := h.ProjectDocs.Delete(r.Context(), docID); err != nil {
		slog.Error("delete doc from form", "id", docID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	slog.Info("project doc deleted via form",
		"id", docID, "project", project.ID, "actor", user.Username)
	http.Redirect(w, r,
		"/spaces/"+space.Slug+"/projects/"+project.Slug+"/docs",
		http.StatusSeeOther)
}

