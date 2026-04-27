package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// ProjectHandler serves both the API endpoints under /api/projects and
// the server-rendered pages under /spaces/{space_slug}/projects. The
// stores below cover both surfaces; the Render / Posts / Reactions /
// EditHistory fields are only used by the page methods (added in
// Sprint 16b) and stay nil when the handler is wired API-only.
type ProjectHandler struct {
	Projects    *model.ProjectStore
	ProjectDocs *model.ProjectDocStore
	Seasons     *model.SeasonStore
	Members     *model.ProjectMemberStore
	Spaces      *model.SpaceStore
	Tags        *model.TagStore
	Users       *model.UserStore
	Markdown    *render.Markdown
	Sanitizer   *render.Sanitizer

	// Sprint 16b page-rendering dependencies.
	Render      *render.Engine
	Posts       *model.PostStore
	Reactions   *model.ReactionStore
	EditHistory *model.PostEditHistoryStore
}

// Length / count limits enforced server-side. The compose UI (16b)
// will mirror these so the user can't submit a body that the server
// will reject.
const (
	maxProjectNameLen   = 80
	maxProjectDescLen   = 500
	maxProjectIconLen   = 16
	maxProjectColorLen  = 16
	maxProjectTagsCount = 10

	maxDocTitleLen     = 120
	maxDocContentLen   = 100_000
	maxSeasonTitleLen  = 120
	maxSeasonDescLen   = 2000
	maxClosingDocLen   = 100_000
)

// ============================================================
// Project endpoints
// ============================================================

type createProjectRequest struct {
	Slug        string  `json:"slug"`
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Visibility  string  `json:"visibility,omitempty"`
	Icon        string  `json:"icon,omitempty"`
	Color       string  `json:"color,omitempty"`
	TagIDs      []int64 `json:"tag_ids,omitempty"`
}

// CreateInSpace handles POST /api/spaces/{space_slug}/projects.
// Requires RequireAuth + RequireOwner (>= 100); the route group in
// main.go enforces both before this method runs.
func (h *ProjectHandler) CreateInSpace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	spaceSlug := chi.URLParam(r, "space_slug")
	space, err := h.Spaces.FindBySlug(r.Context(), spaceSlug)
	if err != nil {
		slog.Error("create project: find space", "slug", spaceSlug, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if space == nil {
		writeError(w, http.StatusNotFound, "space not found")
		return
	}

	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	if req.Name == "" || len(req.Name) > maxProjectNameLen {
		writeError(w, http.StatusBadRequest, "name must be 1-80 characters")
		return
	}
	if len(req.Description) > maxProjectDescLen {
		writeError(w, http.StatusBadRequest, "description too long")
		return
	}
	if len(req.Icon) > maxProjectIconLen || len(req.Color) > maxProjectColorLen {
		writeError(w, http.StatusBadRequest, "icon or color too long")
		return
	}
	if len(req.TagIDs) > maxProjectTagsCount {
		writeError(w, http.StatusBadRequest, "too many tags")
		return
	}

	if req.Slug == "" {
		req.Slug = model.SlugifyProject(req.Name)
	}
	if err := model.ValidateProjectSlug(req.Slug); err != nil {
		writeError(w, http.StatusBadRequest, "slug must be 3-64 chars, lowercase letters/digits/hyphens")
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = model.ProjectVisibilityPublic
	}
	if !validProjectVisibility(visibility) {
		writeError(w, http.StatusBadRequest, "visibility must be public, members_only, or hidden")
		return
	}

	project, err := h.Projects.Create(r.Context(), model.ProjectCreateParams{
		SpaceID:     space.ID,
		Slug:        req.Slug,
		Name:        req.Name,
		Description: req.Description,
		Status:      model.ProjectStatusDraft,
		Visibility:  visibility,
		OwnerID:     user.ID,
		Icon:        req.Icon,
		Color:       req.Color,
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrProjectInvalidSlug):
			writeError(w, http.StatusBadRequest, "invalid slug")
		case errors.Is(err, model.ErrProjectSlugTaken):
			writeError(w, http.StatusConflict, "slug already taken in this space")
		default:
			slog.Error("create project", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}

	if len(req.TagIDs) > 0 {
		if err := h.Projects.AttachTags(r.Context(), project.ID, req.TagIDs); err != nil {
			slog.Warn("create project: attach tags", "project", project.ID, "error", err)
		} else if tags, err := h.Projects.ListTags(r.Context(), project.ID); err == nil {
			project.Tags = tags
		}
	}

	slog.Info("project created",
		"id", project.ID, "slug", project.Slug,
		"space", spaceSlug, "owner", user.Username)

	writeJSON(w, http.StatusCreated, project)
}

// ListInSpace handles GET /api/spaces/{space_slug}/projects. Returns
// projects the viewer is allowed to see (public always; members-only
// and hidden when the viewer has a member row).
func (h *ProjectHandler) ListInSpace(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	var viewerID int64
	if user != nil {
		viewerID = user.ID
	}

	spaceSlug := chi.URLParam(r, "space_slug")
	space, err := h.Spaces.FindBySlug(r.Context(), spaceSlug)
	if err != nil {
		slog.Error("list projects: find space", "slug", spaceSlug, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if space == nil {
		writeError(w, http.StatusNotFound, "space not found")
		return
	}

	projects, err := h.Projects.ListBySpace(r.Context(), space.ID, viewerID)
	if err != nil {
		slog.Error("list projects", "space", space.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"projects": projects,
		"space":    space,
	})
}

// Get handles GET /api/projects/{project_id}. Visibility-checked.
func (h *ProjectHandler) Get(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	if tags, err := h.Projects.ListTags(r.Context(), project.ID); err == nil {
		project.Tags = tags
	}
	writeJSON(w, http.StatusOK, project)
}

type updateProjectRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Status      *string `json:"status,omitempty"`
	Visibility  *string `json:"visibility,omitempty"`
	Icon        *string `json:"icon,omitempty"`
	Color       *string `json:"color,omitempty"`
}

// Update handles PATCH /api/projects/{project_id}. Owner-or-admin only.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}

	var req updateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	params := model.ProjectUpdateParams{
		ID:          project.ID,
		Name:        project.Name,
		Description: project.Description,
		Status:      project.Status,
		Visibility:  project.Visibility,
		Icon:        project.Icon,
		Color:       project.Color,
	}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len(name) > maxProjectNameLen {
			writeError(w, http.StatusBadRequest, "name must be 1-80 characters")
			return
		}
		params.Name = name
	}
	if req.Description != nil {
		if len(*req.Description) > maxProjectDescLen {
			writeError(w, http.StatusBadRequest, "description too long")
			return
		}
		params.Description = *req.Description
	}
	if req.Status != nil {
		if !validProjectStatus(*req.Status) {
			writeError(w, http.StatusBadRequest, "invalid status")
			return
		}
		params.Status = *req.Status
	}
	if req.Visibility != nil {
		if !validProjectVisibility(*req.Visibility) {
			writeError(w, http.StatusBadRequest, "invalid visibility")
			return
		}
		params.Visibility = *req.Visibility
	}
	if req.Icon != nil {
		if len(*req.Icon) > maxProjectIconLen {
			writeError(w, http.StatusBadRequest, "icon too long")
			return
		}
		params.Icon = *req.Icon
	}
	if req.Color != nil {
		if len(*req.Color) > maxProjectColorLen {
			writeError(w, http.StatusBadRequest, "color too long")
			return
		}
		params.Color = *req.Color
	}

	if err := h.Projects.Update(r.Context(), params); err != nil {
		if errors.Is(err, model.ErrProjectNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Error("update project", "id", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	updated, err := h.Projects.FindByID(r.Context(), project.ID)
	if err != nil || updated == nil {
		slog.Error("update project: reload", "id", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// Delete handles DELETE /api/projects/{project_id} as a soft-delete.
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	if err := h.Projects.SoftDelete(r.Context(), project.ID); err != nil {
		if errors.Is(err, model.ErrProjectNotFound) {
			writeError(w, http.StatusNotFound, "project not found")
			return
		}
		slog.Error("delete project", "id", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Doc endpoints
// ============================================================

type upsertDocRequest struct {
	Title     string `json:"title"`
	ContentMD string `json:"content_md"`
	SortOrder int    `json:"sort_order,omitempty"`
}

// ListDocs handles GET /api/projects/{project_id}/docs.
func (h *ProjectHandler) ListDocs(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	docs, err := h.ProjectDocs.ListByProject(r.Context(), project.ID)
	if err != nil {
		slog.Error("list project docs", "project", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"docs": docs})
}

// GetDoc handles GET /api/projects/{project_id}/docs/{doc_type} for the
// four canonical types. Custom docs are accessible via the list call.
func (h *ProjectHandler) GetDoc(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	docType := chi.URLParam(r, "doc_type")
	doc, err := h.ProjectDocs.GetByType(r.Context(), project.ID, docType)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrProjectDocNotFound):
			writeError(w, http.StatusNotFound, "doc not found")
		case errors.Is(err, model.ErrProjectDocInvalidType):
			writeError(w, http.StatusBadRequest, "invalid doc type")
		default:
			slog.Error("get project doc", "project", project.ID, "type", docType, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

// UpsertDoc handles PUT /api/projects/{project_id}/docs/{doc_type}.
// Owner / contributor / admin only.
func (h *ProjectHandler) UpsertDoc(w http.ResponseWriter, r *http.Request) {
	project, user, ok := h.loadProjectForEdit(w, r)
	if !ok {
		return
	}
	docType := chi.URLParam(r, "doc_type")
	if !model.IsValidProjectDocType(docType) {
		writeError(w, http.StatusBadRequest, "invalid doc type")
		return
	}

	var req upsertDocRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Title) > maxDocTitleLen {
		writeError(w, http.StatusBadRequest, "title too long")
		return
	}
	if len(req.ContentMD) > maxDocContentLen {
		writeError(w, http.StatusBadRequest, "content too long")
		return
	}

	contentHTML := h.renderMarkdown(req.ContentMD)

	doc, err := h.ProjectDocs.Upsert(r.Context(), model.ProjectDocUpsertParams{
		ProjectID:   project.ID,
		DocType:     docType,
		Title:       req.Title,
		ContentMD:   req.ContentMD,
		ContentHTML: contentHTML,
		SortOrder:   req.SortOrder,
		EditedBy:    user.ID,
	})
	if err != nil {
		if errors.Is(err, model.ErrProjectDocInvalidType) {
			writeError(w, http.StatusBadRequest, "invalid doc type")
			return
		}
		slog.Error("upsert project doc", "project", project.ID, "type", docType, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("project doc upsert",
		"project", project.ID, "type", docType, "editor", user.Username)
	writeJSON(w, http.StatusOK, doc)
}

// DeleteDoc handles DELETE /api/projects/{project_id}/docs/{doc_id}.
// Only custom docs can be deleted; canonical docs stay forever (their
// content is editable to empty, but the slot is reserved).
func (h *ProjectHandler) DeleteDoc(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	docID, err := strconv.ParseInt(chi.URLParam(r, "doc_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid doc id")
		return
	}
	doc, err := h.ProjectDocs.GetByID(r.Context(), docID)
	if err != nil {
		if errors.Is(err, model.ErrProjectDocNotFound) {
			writeError(w, http.StatusNotFound, "doc not found")
			return
		}
		slog.Error("delete project doc: load", "id", docID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if doc.ProjectID != project.ID {
		writeError(w, http.StatusNotFound, "doc not found")
		return
	}
	if doc.DocType != model.ProjectDocCustom {
		writeError(w, http.StatusBadRequest, "only custom docs can be deleted")
		return
	}
	if err := h.ProjectDocs.Delete(r.Context(), docID); err != nil {
		if errors.Is(err, model.ErrProjectDocNotFound) {
			writeError(w, http.StatusNotFound, "doc not found")
			return
		}
		slog.Error("delete project doc", "id", docID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Season endpoints
// ============================================================

type createSeasonRequest struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

type updateSeasonRequest struct {
	Title       *string `json:"title,omitempty"`
	Description *string `json:"description,omitempty"`
}

type closeSeasonRequest struct {
	ClosingDocMD string `json:"closing_doc_md"`
}

// CreateSeason handles POST /api/projects/{project_id}/seasons.
func (h *ProjectHandler) CreateSeason(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	var req createSeasonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" || len(req.Title) > maxSeasonTitleLen {
		writeError(w, http.StatusBadRequest, "title must be 1-120 characters")
		return
	}
	if len(req.Description) > maxSeasonDescLen {
		writeError(w, http.StatusBadRequest, "description too long")
		return
	}
	season, err := h.Seasons.Create(r.Context(), model.SeasonCreateParams{
		ProjectID:   project.ID,
		Title:       req.Title,
		Description: req.Description,
	})
	if err != nil {
		slog.Error("create season", "project", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("season created",
		"project", project.ID, "season", season.SeasonNumber, "id", season.ID)
	writeJSON(w, http.StatusCreated, season)
}

// ListSeasons handles GET /api/projects/{project_id}/seasons.
func (h *ProjectHandler) ListSeasons(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	seasons, err := h.Seasons.ListByProject(r.Context(), project.ID)
	if err != nil {
		slog.Error("list seasons", "project", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seasons": seasons})
}

// GetSeason handles GET /api/projects/{project_id}/seasons/{number}.
func (h *ProjectHandler) GetSeason(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	season, ok := h.loadSeasonForProject(w, r, project)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, season)
}

// UpdateSeason handles PATCH /api/projects/{project_id}/seasons/{number}.
func (h *ProjectHandler) UpdateSeason(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	season, ok := h.loadSeasonForProject(w, r, project)
	if !ok {
		return
	}
	var req updateSeasonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	title := season.Title
	desc := season.Description
	if req.Title != nil {
		t := strings.TrimSpace(*req.Title)
		if t == "" || len(t) > maxSeasonTitleLen {
			writeError(w, http.StatusBadRequest, "title must be 1-120 characters")
			return
		}
		title = t
	}
	if req.Description != nil {
		if len(*req.Description) > maxSeasonDescLen {
			writeError(w, http.StatusBadRequest, "description too long")
			return
		}
		desc = *req.Description
	}
	if err := h.Seasons.UpdateMeta(r.Context(), season.ID, title, desc); err != nil {
		if errors.Is(err, model.ErrSeasonNotFound) {
			writeError(w, http.StatusNotFound, "season not found")
			return
		}
		slog.Error("update season", "id", season.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	updated, err := h.Seasons.FindByID(r.Context(), season.ID)
	if err != nil || updated == nil {
		slog.Error("update season: reload", "id", season.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// ActivateSeason handles POST /api/projects/{project_id}/seasons/{number}/activate.
func (h *ProjectHandler) ActivateSeason(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	season, ok := h.loadSeasonForProject(w, r, project)
	if !ok {
		return
	}
	if err := h.Seasons.Activate(r.Context(), season.ID); err != nil {
		switch {
		case errors.Is(err, model.ErrSeasonNotFound):
			writeError(w, http.StatusNotFound, "season not found")
		case errors.Is(err, model.ErrSeasonNotPlanned):
			writeError(w, http.StatusConflict, "season already activated or closed")
		default:
			slog.Error("activate season", "id", season.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	updated, err := h.Seasons.FindByID(r.Context(), season.ID)
	if err != nil || updated == nil {
		slog.Error("activate season: reload", "id", season.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("season activated", "id", season.ID, "project", project.ID)
	writeJSON(w, http.StatusOK, updated)
}

// CloseSeason handles POST /api/projects/{project_id}/seasons/{number}/close.
func (h *ProjectHandler) CloseSeason(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	season, ok := h.loadSeasonForProject(w, r, project)
	if !ok {
		return
	}
	var req closeSeasonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.ClosingDocMD) == "" {
		writeError(w, http.StatusBadRequest, "closing_doc_md is required")
		return
	}
	if len(req.ClosingDocMD) > maxClosingDocLen {
		writeError(w, http.StatusBadRequest, "closing doc too long")
		return
	}
	closingHTML := h.renderMarkdown(req.ClosingDocMD)
	if err := h.Seasons.Close(r.Context(), season.ID, req.ClosingDocMD, closingHTML); err != nil {
		switch {
		case errors.Is(err, model.ErrSeasonNotFound):
			writeError(w, http.StatusNotFound, "season not found")
		case errors.Is(err, model.ErrSeasonNotActive):
			writeError(w, http.StatusConflict, "season is not active")
		default:
			slog.Error("close season", "id", season.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	updated, err := h.Seasons.FindByID(r.Context(), season.ID)
	if err != nil || updated == nil {
		slog.Error("close season: reload", "id", season.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("season closed", "id", season.ID, "project", project.ID)
	writeJSON(w, http.StatusOK, updated)
}

// ============================================================
// Member endpoints
// ============================================================

type addMemberRequest struct {
	UserID int64  `json:"user_id"`
	Role   string `json:"role"`
}

type updateMemberRequest struct {
	Role string `json:"role"`
}

// ListMembers handles GET /api/projects/{project_id}/members.
func (h *ProjectHandler) ListMembers(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadVisibleProject(w, r)
	if !ok {
		return
	}
	members, err := h.Members.ListByProject(r.Context(), project.ID)
	if err != nil {
		slog.Error("list members", "project", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

// AddMember handles POST /api/projects/{project_id}/members.
func (h *ProjectHandler) AddMember(w http.ResponseWriter, r *http.Request) {
	project, user, ok := h.loadProjectForManageWithUser(w, r)
	if !ok {
		return
	}
	var req addMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.UserID <= 0 {
		writeError(w, http.StatusBadRequest, "user_id required")
		return
	}
	if !model.IsValidProjectRole(req.Role) || req.Role == model.ProjectRoleOwner {
		writeError(w, http.StatusBadRequest, "role must be contributor or viewer")
		return
	}
	target, err := h.Users.FindByID(r.Context(), req.UserID)
	if err != nil {
		slog.Error("add member: find user", "user", req.UserID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target == nil {
		writeError(w, http.StatusBadRequest, "user not found")
		return
	}
	if err := h.Members.Add(r.Context(), project.ID, req.UserID, req.Role, user.ID); err != nil {
		switch {
		case errors.Is(err, model.ErrMemberAlreadyExists):
			writeError(w, http.StatusConflict, "user is already a project member")
		case errors.Is(err, model.ErrInvalidRole):
			writeError(w, http.StatusBadRequest, "invalid role")
		default:
			slog.Error("add member", "project", project.ID, "user", req.UserID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	slog.Info("project member added",
		"project", project.ID, "user", req.UserID, "role", req.Role)
	writeJSON(w, http.StatusCreated, map[string]any{
		"project_id": project.ID,
		"user_id":    req.UserID,
		"role":       req.Role,
	})
}

// UpdateMemberRole handles PATCH /api/projects/{project_id}/members/{user_id}.
func (h *ProjectHandler) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	var req updateMemberRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.Members.UpdateRole(r.Context(), project.ID, userID, req.Role); err != nil {
		switch {
		case errors.Is(err, model.ErrMemberNotFound):
			writeError(w, http.StatusNotFound, "member not found")
		case errors.Is(err, model.ErrCannotAssignOwner):
			writeError(w, http.StatusBadRequest, "ownership transfer is not supported in this endpoint")
		case errors.Is(err, model.ErrInvalidRole):
			writeError(w, http.StatusBadRequest, "invalid role")
		default:
			slog.Error("update member role", "project", project.ID, "user", userID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"project_id": project.ID,
		"user_id":    userID,
		"role":       req.Role,
	})
}

// RemoveMember handles DELETE /api/projects/{project_id}/members/{user_id}.
func (h *ProjectHandler) RemoveMember(w http.ResponseWriter, r *http.Request) {
	project, ok := h.loadProjectForManage(w, r)
	if !ok {
		return
	}
	userID, err := strconv.ParseInt(chi.URLParam(r, "user_id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	if err := h.Members.Remove(r.Context(), project.ID, userID); err != nil {
		switch {
		case errors.Is(err, model.ErrMemberNotFound):
			writeError(w, http.StatusNotFound, "member not found")
		case errors.Is(err, model.ErrCannotRemoveOwner):
			writeError(w, http.StatusBadRequest, "cannot remove the project owner")
		default:
			slog.Error("remove member", "project", project.ID, "user", userID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ============================================================
// Internal helpers
// ============================================================

// loadVisibleProject parses {project_id}, looks the project up, and
// applies visibility rules. Writes the appropriate 4xx response and
// returns ok=false when access is denied or the project is missing.
func (h *ProjectHandler) loadVisibleProject(w http.ResponseWriter, r *http.Request) (*model.Project, bool) {
	project, ok := h.loadProjectByID(w, r)
	if !ok {
		return nil, false
	}
	user := auth.UserFromContext(r.Context())
	var viewerID int64
	if user != nil {
		viewerID = user.ID
	}
	if user != nil && user.PowerLevel >= 75 {
		return project, true
	}
	allowed, err := h.Projects.CanUserAccess(r.Context(), project.ID, viewerID)
	if err != nil {
		slog.Error("project access check", "id", project.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if !allowed {
		// Returning 404 (not 403) so we don't leak that a hidden
		// project exists at this id to non-members.
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// loadProjectForManage loads the project and verifies the caller is
// owner-or-admin. Returns ok=false with a 4xx response when the caller
// can't manage the project.
func (h *ProjectHandler) loadProjectForManage(w http.ResponseWriter, r *http.Request) (*model.Project, bool) {
	p, _, ok := h.loadProjectForManageWithUser(w, r)
	return p, ok
}

func (h *ProjectHandler) loadProjectForManageWithUser(w http.ResponseWriter, r *http.Request) (*model.Project, *model.User, bool) {
	project, ok := h.loadProjectByID(w, r)
	if !ok {
		return nil, nil, false
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, nil, false
	}
	if user.PowerLevel < 75 && project.OwnerID != user.ID {
		writeError(w, http.StatusForbidden, "owner or admin required")
		return nil, nil, false
	}
	return project, user, true
}

// loadProjectForEdit loads the project and verifies the caller is
// owner / contributor / admin (any role that can edit project docs).
func (h *ProjectHandler) loadProjectForEdit(w http.ResponseWriter, r *http.Request) (*model.Project, *model.User, bool) {
	project, ok := h.loadProjectByID(w, r)
	if !ok {
		return nil, nil, false
	}
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return nil, nil, false
	}
	if user.PowerLevel >= 75 || project.OwnerID == user.ID {
		return project, user, true
	}
	canEdit, err := h.userCanEditProject(r.Context(), project.ID, user.ID)
	if err != nil {
		slog.Error("project edit permission", "project", project.ID, "user", user.ID, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, nil, false
	}
	if !canEdit {
		writeError(w, http.StatusForbidden, "contributor, owner, or admin required")
		return nil, nil, false
	}
	return project, user, true
}

func (h *ProjectHandler) userCanEditProject(ctx context.Context, projectID, userID int64) (bool, error) {
	role, err := h.Members.GetRole(ctx, projectID, userID)
	if err != nil {
		if errors.Is(err, model.ErrMemberNotFound) {
			return false, nil
		}
		return false, err
	}
	return role == model.ProjectRoleOwner || role == model.ProjectRoleContributor, nil
}

// loadProjectByID parses {project_id} from the URL, looks it up, and
// writes a 400 / 404 / 500 response when it cannot.
func (h *ProjectHandler) loadProjectByID(w http.ResponseWriter, r *http.Request) (*model.Project, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "project_id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return nil, false
	}
	project, err := h.Projects.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("find project by id", "id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if project == nil {
		writeError(w, http.StatusNotFound, "project not found")
		return nil, false
	}
	return project, true
}

// loadSeasonForProject parses {number}, looks the season up under the
// already-resolved project, and writes a 4xx response on failure.
func (h *ProjectHandler) loadSeasonForProject(w http.ResponseWriter, r *http.Request, project *model.Project) (*model.Season, bool) {
	num, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || num <= 0 {
		writeError(w, http.StatusBadRequest, "invalid season number")
		return nil, false
	}
	season, err := h.Seasons.GetByNumber(r.Context(), project.ID, num)
	if err != nil {
		slog.Error("get season by number", "project", project.ID, "num", num, "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return nil, false
	}
	if season == nil {
		writeError(w, http.StatusNotFound, "season not found")
		return nil, false
	}
	return season, true
}

// renderMarkdown runs goldmark + bluemonday on raw markdown. Returns
// the empty string when neither dependency is wired up (test wiring).
func (h *ProjectHandler) renderMarkdown(md string) string {
	if md == "" {
		return ""
	}
	if h.Markdown == nil {
		return ""
	}
	html, err := h.Markdown.Render(md)
	if err != nil {
		return ""
	}
	if h.Sanitizer != nil {
		html = h.Sanitizer.Clean(html)
	}
	return html
}

func validProjectStatus(s string) bool {
	switch s {
	case model.ProjectStatusDraft, model.ProjectStatusActive,
		model.ProjectStatusArchived, model.ProjectStatusClosed:
		return true
	}
	return false
}

func validProjectVisibility(v string) bool {
	switch v {
	case model.ProjectVisibilityPublic, model.ProjectVisibilityMembersOnly,
		model.ProjectVisibilityHidden:
		return true
	}
	return false
}
