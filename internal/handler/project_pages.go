package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

// Sprint 16b: server-rendered pages for the project system. The
// methods below live on the same ProjectHandler that serves the API
// from project.go - they share the same store dependencies plus the
// Render / Posts / Reactions / EditHistory fields added for page work.
//
// URL shape:
//   /spaces/{space_slug}/projects                          -> ListPage
//   /spaces/{space_slug}/projects/{project_slug}           -> DetailPage (Overview tab)
//   /spaces/{space_slug}/projects/{project_slug}/docs      -> DocsPage
//   /spaces/{space_slug}/projects/{project_slug}/docs/{doc_type} -> DocPage
//   /spaces/{space_slug}/projects/{project_slug}/seasons   -> SeasonsPage
//   /spaces/{space_slug}/projects/{project_slug}/seasons/{number} -> SeasonPage
//   /spaces/{space_slug}/projects/{project_slug}/members   -> MembersPage
//
// Hidden / members-only projects return 404 to non-members rather
// than 403 so we don't leak existence at this URL. Admins
// (power_level >= 75) bypass the visibility filter.

// ============================================================
// Page handlers
// ============================================================

// ListPage renders /spaces/{space_slug}/projects. Authenticated users
// see public projects always, plus members-only / hidden projects
// where they have a project_members row.
func (h *ProjectHandler) ListPage(w http.ResponseWriter, r *http.Request) {
	space, ok := h.loadSpaceForPage(w, r)
	if !ok {
		return
	}

	user := auth.UserFromContext(r.Context())
	var viewerID int64
	if user != nil {
		viewerID = user.ID
	}

	projects, err := h.Projects.ListBySpace(r.Context(), space.ID, viewerID)
	if err != nil {
		slog.Error("project list page: list", "space", space.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if projects == nil {
		projects = []model.Project{}
	}

	// Optional status filter from query string, e.g. ?status=active.
	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" && validProjectStatus(statusFilter) {
		filtered := projects[:0]
		for _, p := range projects {
			if p.Status == statusFilter {
				filtered = append(filtered, p)
			}
		}
		projects = filtered
	} else {
		statusFilter = ""
	}

	// Per-project owner display + tag list. One round trip per project
	// is fine here - the list is bounded and the lookups are cheap.
	owners := make(map[int64]*model.User, len(projects))
	tagsByProject := make(map[int64][]model.Tag, len(projects))
	memberCount := make(map[int64]int, len(projects))
	for _, p := range projects {
		if h.Users != nil {
			if u, err := h.Users.FindByID(r.Context(), p.OwnerID); err == nil && u != nil {
				owners[p.OwnerID] = u
			}
		}
		if tags, err := h.Projects.ListTags(r.Context(), p.ID); err == nil {
			tagsByProject[p.ID] = tags
		}
		if h.Members != nil {
			if ms, err := h.Members.ListByProject(r.Context(), p.ID); err == nil {
				memberCount[p.ID] = len(ms)
			}
		}
	}

	canCreate := user != nil && user.PowerLevel >= 100

	data := h.newProjectPageData(r, space.Name+" projects - GoLab", space)
	data["Content"] = map[string]any{
		"Space":         space,
		"Projects":      projects,
		"Owners":        owners,
		"TagsByProject": tagsByProject,
		"MemberCount":   memberCount,
		"StatusFilter":  statusFilter,
		"CanCreate":     canCreate,
	}
	if err := h.Render.Render(w, "project-list", data); err != nil {
		slog.Error("render project-list", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// DetailPage renders /spaces/{space_slug}/projects/{project_slug} as
// the Overview tab. Sprint 16b polish: also pulls project stats for
// the doc-status, season-timeline, activity-heatmap, and
// posts-per-season-chart panels on the dashboard.
func (h *ProjectHandler) DetailPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}

	docs, _ := h.ProjectDocs.ListByProject(r.Context(), project.ID)
	seasons, _ := h.Seasons.ListByProject(r.Context(), project.ID)
	members, _ := h.Members.ListByProject(r.Context(), project.ID)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)

	user := auth.UserFromContext(r.Context())
	owner := h.lookupUser(r, project.OwnerID)
	canManage := h.canUserManageProject(user, project)
	canEdit := canManage || h.canUserEditProject(r, user, project)

	docPresence := projectDocPresence(docs)
	currentSeason := currentActiveSeason(seasons)

	// Sprint 16d: load sub-projects when the user can see any. Both
	// queries respect visibility - a viewer who can see the parent
	// but not a hidden child won't see that child here. Parent stats
	// are only meaningful when there's at least one child.
	var (
		viewerID         int64
		children         []model.Project
		parentStats      *model.ParentProjectStats
		canCreateProject = user != nil && user.PowerLevel >= 100
	)
	if user != nil {
		viewerID = user.ID
	}
	if c, err := h.Projects.ListChildProjects(r.Context(), project.ID, viewerID); err == nil {
		children = c
	} else {
		slog.Warn("project detail: list children", "id", project.ID, "error", err)
	}
	if len(children) > 0 {
		if ps, err := h.Projects.GetParentProjectStats(r.Context(), project.ID, viewerID); err == nil {
			parentStats = ps
		} else {
			slog.Warn("project detail: parent stats", "id", project.ID, "error", err)
		}
	}

	// Dashboard aggregates. GetProjectStats runs three indexed
	// aggregate queries; failure logs and falls back to empty state
	// so the rest of the page still renders.
	var stats *model.ProjectStats
	if s, err := h.Projects.GetProjectStats(r.Context(), project.ID); err == nil {
		stats = s
	} else {
		slog.Warn("project detail: stats", "id", project.ID, "error", err)
	}

	var heatmap projectHeatmap
	var seasonsChart projectSeasonsChart
	totalPosts := 0
	totalContributors := 0
	activeDays := 0
	docsCompleted := 0
	if stats != nil {
		heatmap = buildProjectHeatmap(stats.PostCountsByDay)
		seasonsChart = buildProjectSeasonsChart(stats.PostCountsBySeason, project.Color)
		totalPosts = stats.TotalPosts
		totalContributors = stats.TotalContributors
		activeDays = stats.ActiveDays
		docsCompleted = stats.DocsCompleted
	}

	data := h.newProjectPageData(r, project.Name+" - GoLab", space)
	data["Content"] = map[string]any{
		"Space":             space,
		"Project":           project,
		"Owner":             owner,
		"Tags":              tags,
		"Docs":              docs,
		"Seasons":           seasons,
		"Members":           members,
		"DocPresence":       docPresence,
		"CurrentSeason":     currentSeason,
		"ActiveTab":         "overview",
		"CanEdit":           canEdit,
		"CanManage":         canManage,
		"Heatmap":           heatmap,
		"SeasonsChart":      seasonsChart,
		"TotalPosts":        totalPosts,
		"TotalContributors": totalContributors,
		"ActiveDays":        activeDays,
		"DocsCompleted":     docsCompleted,
		// Sprint 16d sub-projects.
		"Children":         children,
		"ParentStats":      parentStats,
		"CanCreateProject": canCreateProject,
	}
	if err := h.Render.Render(w, "project-show", data); err != nil {
		slog.Error("render project-show", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// DocsPage renders the Docs tab: list of all docs (canonical first
// then custom by sort_order).
func (h *ProjectHandler) DocsPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	docs, _ := h.ProjectDocs.ListByProject(r.Context(), project.ID)

	user := auth.UserFromContext(r.Context())
	owner := h.lookupUser(r, project.OwnerID)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)
	canManage := h.canUserManageProject(user, project)
	canEdit := canManage || h.canUserEditProject(r, user, project)

	data := h.newProjectPageData(r, project.Name+" docs - GoLab", space)
	data["Content"] = map[string]any{
		"Space":     space,
		"Project":   project,
		"Owner":     owner,
		"Tags":      tags,
		"Docs":      sortedDocs(docs),
		"ActiveTab": "docs",
		"CanEdit":   canEdit,
		"CanManage": canManage,
	}
	if err := h.Render.Render(w, "project-docs", data); err != nil {
		slog.Error("render project-docs", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// DocPage renders a single doc by type. Canonical types
// (concept/architecture/workflow/roadmap) are addressed by their
// type slug; custom docs are addressed by id but for read-only view
// in 16b we accept canonical types only.
func (h *ProjectHandler) DocPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	docType := chi.URLParam(r, "doc_type")
	if !model.IsValidProjectDocType(docType) || docType == model.ProjectDocCustom {
		http.NotFound(w, r)
		return
	}

	doc, err := h.ProjectDocs.GetByType(r.Context(), project.ID, docType)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	owner := h.lookupUser(r, project.OwnerID)
	editor := h.lookupUserPtr(r, doc.LastEditedBy)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)
	user := auth.UserFromContext(r.Context())
	canManage := h.canUserManageProject(user, project)
	canEdit := canManage || h.canUserEditProject(r, user, project)

	data := h.newProjectPageData(r, doc.Title+" - "+project.Name, space)
	data["Content"] = map[string]any{
		"Space":     space,
		"Project":   project,
		"Owner":     owner,
		"Tags":      tags,
		"Doc":       doc,
		"Editor":    editor,
		"DocLabel":  docTypeLabel(docType),
		"ActiveTab": "docs",
		"CanEdit":   canEdit,
		"CanManage": canManage,
	}
	if err := h.Render.Render(w, "project-doc", data); err != nil {
		slog.Error("render project-doc", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// SeasonsPage renders the Seasons tab as a vertical timeline.
func (h *ProjectHandler) SeasonsPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	seasons, _ := h.Seasons.ListByProject(r.Context(), project.ID)
	owner := h.lookupUser(r, project.OwnerID)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)

	postCounts := make(map[int64]int, len(seasons))
	if h.Posts != nil {
		for _, se := range seasons {
			if posts, err := h.Posts.ListBySeason(r.Context(), se.ID, 1000, 0); err == nil {
				postCounts[se.ID] = len(posts)
			}
		}
	}

	user := auth.UserFromContext(r.Context())
	canManage := h.canUserManageProject(user, project)

	data := h.newProjectPageData(r, project.Name+" seasons - GoLab", space)
	data["Content"] = map[string]any{
		"Space":      space,
		"Project":    project,
		"Owner":      owner,
		"Tags":       tags,
		"Seasons":    reverseSeasons(seasons),
		"PostCounts": postCounts,
		"ActiveTab":  "seasons",
		"CanManage":  canManage,
	}
	if err := h.Render.Render(w, "project-seasons", data); err != nil {
		slog.Error("render project-seasons", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// SeasonPage renders a single season with the posts assigned to it.
// Sprint 16b polish: also computes the four KPI numbers, a 30-day
// posts-over-time line chart, and a donut chart of posts by type so
// the season detail page reads as a mini dashboard.
func (h *ProjectHandler) SeasonPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	num, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || num <= 0 {
		http.NotFound(w, r)
		return
	}
	season, err := h.Seasons.GetByNumber(r.Context(), project.ID, num)
	if err != nil {
		slog.Error("season page: get", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if season == nil {
		http.NotFound(w, r)
		return
	}

	var posts []model.Post
	if h.Posts != nil {
		posts, err = h.Posts.ListBySeason(r.Context(), season.ID, 50, 0)
		if err != nil {
			slog.Error("season page: list posts", "error", err)
			posts = nil
		}
	}
	if posts == nil {
		posts = []model.Post{}
	}
	h.hydratePostsForCard(r, posts)

	owner := h.lookupUser(r, project.OwnerID)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)
	user := auth.UserFromContext(r.Context())
	canManage := h.canUserManageProject(user, project)

	// Dashboard aggregates. Three small queries via GetSeasonStats;
	// failures degrade to empty panels rather than 500-ing the page.
	var stats *model.SeasonStats
	if s, err := h.Seasons.GetSeasonStats(r.Context(), season.ID); err == nil {
		stats = s
	} else {
		slog.Warn("season page: stats", "id", season.ID, "error", err)
	}

	var dailyChart seasonDailyChart
	var typeChart seasonTypeChart
	postCount := 0
	contributorCount := 0
	daysRunning := 0
	linkedDocs := 0
	if stats != nil {
		dailyChart = buildSeasonDailyChart(stats.PostCountsByDay)
		typeChart = buildSeasonTypeChart(stats.PostCountsByType)
		postCount = stats.PostCount
		contributorCount = stats.ContributorCount
		daysRunning = stats.DaysRunning
		linkedDocs = stats.LinkedDocsCount
	}

	data := h.newProjectPageData(r, "Season "+strconv.Itoa(num)+" - "+project.Name, space)
	data["Content"] = map[string]any{
		"Space":            space,
		"Project":          project,
		"Owner":            owner,
		"Tags":             tags,
		"Season":           season,
		"Posts":            posts,
		"ActiveTab":        "seasons",
		"CanManage":        canManage,
		"PostCount":        postCount,
		"ContributorCount": contributorCount,
		"DaysRunning":      daysRunning,
		"LinkedDocs":       linkedDocs,
		"DailyChart":       dailyChart,
		"TypeChart":        typeChart,
	}
	if err := h.Render.Render(w, "project-season", data); err != nil {
		slog.Error("render project-season", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// MembersPage renders the read-only Members tab. Add / remove UI
// lands in 16c.
func (h *ProjectHandler) MembersPage(w http.ResponseWriter, r *http.Request) {
	space, project, ok := h.loadProjectByPath(w, r)
	if !ok {
		return
	}
	members, _ := h.Members.ListByProject(r.Context(), project.ID)
	owner := h.lookupUser(r, project.OwnerID)
	tags, _ := h.Projects.ListTags(r.Context(), project.ID)
	user := auth.UserFromContext(r.Context())
	canManage := h.canUserManageProject(user, project)

	owners, contributors, viewers := splitMembersByRole(members)

	data := h.newProjectPageData(r, project.Name+" members - GoLab", space)
	data["Content"] = map[string]any{
		"Space":        space,
		"Project":      project,
		"Owner":        owner,
		"Tags":         tags,
		"Owners":       owners,
		"Contributors": contributors,
		"Viewers":      viewers,
		"ActiveTab":    "members",
		"CanManage":    canManage,
	}
	if err := h.Render.Render(w, "project-members", data); err != nil {
		slog.Error("render project-members", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// ============================================================
// Page helpers
// ============================================================

// loadSpaceForPage resolves space_slug from chi and writes the
// appropriate 4xx / 5xx on failure. Returns ok=false in that case.
func (h *ProjectHandler) loadSpaceForPage(w http.ResponseWriter, r *http.Request) (*model.Space, bool) {
	slug := chi.URLParam(r, "space_slug")
	space, err := h.Spaces.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("page: find space", "slug", slug, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if space == nil {
		http.NotFound(w, r)
		return nil, false
	}
	return space, true
}

// loadProjectByPath resolves space_slug + project_slug into the
// matching space and project, applying the visibility filter for the
// current viewer. Hidden projects return 404 (not 403) for non-members
// so we don't leak existence at this URL; admins
// (power_level >= 75) bypass the visibility check entirely.
func (h *ProjectHandler) loadProjectByPath(w http.ResponseWriter, r *http.Request) (*model.Space, *model.Project, bool) {
	space, ok := h.loadSpaceForPage(w, r)
	if !ok {
		return nil, nil, false
	}
	projectSlug := chi.URLParam(r, "project_slug")
	project, err := h.Projects.FindBySlug(r.Context(), space.ID, projectSlug)
	if err != nil {
		slog.Error("page: find project", "slug", projectSlug, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	if project == nil {
		http.NotFound(w, r)
		return nil, nil, false
	}

	user := auth.UserFromContext(r.Context())
	if user != nil && user.PowerLevel >= 75 {
		return space, project, true
	}
	var viewerID int64
	if user != nil {
		viewerID = user.ID
	}
	allowed, err := h.Projects.CanUserAccess(r.Context(), project.ID, viewerID)
	if err != nil {
		slog.Error("page: project access", "id", project.ID, "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, nil, false
	}
	if !allowed {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return space, project, true
}

// canUserManageProject returns true when the user can mutate the
// project (owner or admin power level).
func (h *ProjectHandler) canUserManageProject(user *model.User, project *model.Project) bool {
	if user == nil {
		return false
	}
	if user.PowerLevel >= 75 {
		return true
	}
	return project.OwnerID == user.ID
}

// canUserEditProject returns true when the user can edit project docs
// (contributor / owner / admin).
func (h *ProjectHandler) canUserEditProject(r *http.Request, user *model.User, project *model.Project) bool {
	if user == nil {
		return false
	}
	if user.PowerLevel >= 75 || project.OwnerID == user.ID {
		return true
	}
	if h.Members == nil {
		return false
	}
	role, err := h.Members.GetRole(r.Context(), project.ID, user.ID)
	if err != nil {
		return false
	}
	return role == model.ProjectRoleOwner || role == model.ProjectRoleContributor
}

func (h *ProjectHandler) lookupUser(r *http.Request, id int64) *model.User {
	if h.Users == nil || id == 0 {
		return nil
	}
	u, err := h.Users.FindByID(r.Context(), id)
	if err != nil {
		return nil
	}
	return u
}

func (h *ProjectHandler) lookupUserPtr(r *http.Request, id *int64) *model.User {
	if id == nil {
		return nil
	}
	return h.lookupUser(r, *id)
}

// hydratePostsForCard attaches tags, reactions and edited_at to each
// post so post-card.html renders identically to the space / feed
// pages. Mirrors the SpaceHandler hydration.
func (h *ProjectHandler) hydratePostsForCard(r *http.Request, posts []model.Post) {
	if h.Tags != nil {
		for i := range posts {
			if tags, err := h.Tags.ListByPost(r.Context(), posts[i].ID); err == nil {
				posts[i].Tags = tags
			}
		}
	}
	if h.Reactions != nil {
		var viewerID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			viewerID = u.ID
		}
		if err := h.Reactions.AttachTo(r.Context(), viewerID, posts); err != nil {
			slog.Warn("project page: attach reactions", "error", err)
		}
	}
	if h.EditHistory != nil {
		if err := h.EditHistory.AttachEditedAt(r.Context(), posts); err != nil {
			slog.Warn("project page: attach edited_at", "error", err)
		}
	}
}

// newProjectPageData mirrors SpaceHandler.newPageData so base.html's
// space bar lights up the right space pill on every project page.
func (h *ProjectHandler) newProjectPageData(r *http.Request, title string, space *model.Space) map[string]any {
	data := map[string]any{
		"Title":        title,
		"SiteName":     "GoLab",
		"User":         auth.UserFromContext(r.Context()),
		"CurrentPath":  r.URL.Path,
		"CurrentSpace": "",
	}
	if space != nil {
		data["CurrentSpace"] = space.Slug
	}
	if h.Spaces != nil {
		if spaces, err := h.Spaces.List(r.Context()); err == nil {
			data["Spaces"] = spaces
		}
	}
	return data
}

// projectDocPresence returns a map of doc-type -> bool for the four
// canonical types so the overview template can render preview cards
// (or "create this doc" CTAs) without a per-type lookup loop.
func projectDocPresence(docs []model.ProjectDoc) map[string]*model.ProjectDoc {
	out := map[string]*model.ProjectDoc{}
	for i := range docs {
		d := docs[i]
		if d.DocType == model.ProjectDocCustom {
			continue
		}
		out[d.DocType] = &d
	}
	return out
}

// sortedDocs places canonical docs first in the briefing's order
// (Concept, Architecture, Workflow, Roadmap), then custom docs by
// sort_order then created_at (already returned in that order by the
// store so we just stable-sort canonical to the front).
func sortedDocs(docs []model.ProjectDoc) []model.ProjectDoc {
	order := map[string]int{
		model.ProjectDocConcept:      0,
		model.ProjectDocArchitecture: 1,
		model.ProjectDocWorkflow:     2,
		model.ProjectDocRoadmap:      3,
	}
	canon := make([]model.ProjectDoc, 0, len(docs))
	custom := make([]model.ProjectDoc, 0, len(docs))
	for _, d := range docs {
		if _, ok := order[d.DocType]; ok {
			canon = append(canon, d)
		} else {
			custom = append(custom, d)
		}
	}
	// Stable bubble sort by canonical order; tiny n.
	for i := 0; i < len(canon); i++ {
		for j := i + 1; j < len(canon); j++ {
			if order[canon[j].DocType] < order[canon[i].DocType] {
				canon[i], canon[j] = canon[j], canon[i]
			}
		}
	}
	return append(canon, custom...)
}

// docTypeLabel returns a human-readable label for the canonical doc
// types. Custom docs render their own Title field.
func docTypeLabel(t string) string {
	switch t {
	case model.ProjectDocConcept:
		return "Concept"
	case model.ProjectDocArchitecture:
		return "Architecture"
	case model.ProjectDocWorkflow:
		return "Workflow"
	case model.ProjectDocRoadmap:
		return "Roadmap"
	}
	return t
}

// currentActiveSeason returns a pointer to the first active season
// in the list, or nil. Used by the overview tab to surface the
// "current season" stat.
func currentActiveSeason(seasons []model.Season) *model.Season {
	for i := range seasons {
		if seasons[i].Status == model.SeasonStatusActive {
			return &seasons[i]
		}
	}
	return nil
}

// reverseSeasons returns seasons newest-first. The store returns them
// ascending by season_number; the timeline UI shows newest at the top.
func reverseSeasons(seasons []model.Season) []model.Season {
	out := make([]model.Season, len(seasons))
	for i, s := range seasons {
		out[len(seasons)-1-i] = s
	}
	return out
}

// splitMembersByRole partitions the member list into the three role
// buckets so the template can render them as separate sections.
func splitMembersByRole(members []model.ProjectMember) (owners, contributors, viewers []model.ProjectMember) {
	for _, m := range members {
		switch m.Role {
		case model.ProjectRoleOwner:
			owners = append(owners, m)
		case model.ProjectRoleContributor:
			contributors = append(contributors, m)
		case model.ProjectRoleViewer:
			viewers = append(viewers, m)
		}
	}
	return
}

// timeAgoOrEmpty is a small alternative to render.timeAgo used inline
// where we'd otherwise need a template function call - keeps the
// project overview's "Last activity" line nil-safe.
//
//nolint:unused // reserved for the overview template; left in place
// so future commits don't have to re-introduce it.
func timeAgoOrEmpty(t *time.Time) string {
	if t == nil {
		return ""
	}
	d := time.Since(*t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h ago"
	default:
		return t.Format("Jan 2, 2006")
	}
}
