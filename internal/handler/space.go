package handler

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// SpaceHandler serves both the /s/:slug HTML page and the /api/spaces
// JSON endpoint.
type SpaceHandler struct {
	Render      *render.Engine
	Spaces      *model.SpaceStore
	Posts       *model.PostStore
	Tags        *model.TagStore
	Reactions   *model.ReactionStore            // Sprint 14: batch-attach reaction state
	EditHistory *model.PostEditHistoryStore     // Sprint 15a B6: batch-attach edited_at
}

// validPostTypeQuery matches the briefing: all six post types from the
// compose box plus "" for no filter.
func isKnownPostType(s string) bool {
	switch s {
	case "discussion", "question", "tutorial", "code", "showcase", "link", "announcement":
		return true
	}
	return false
}

// SpacePage renders /s/:slug with the full space layout: header, type
// filter tabs, post list, popular tags sidebar.
func (h *SpaceHandler) SpacePage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	sp, err := h.Spaces.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("space page: find", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if sp == nil {
		http.NotFound(w, r)
		return
	}

	postType := r.URL.Query().Get("type")
	if postType != "" && !isKnownPostType(postType) {
		postType = ""
	}
	tagFilter := r.URL.Query().Get("tag")

	const pageSize = 30
	var posts []model.Post
	if tagFilter != "" {
		posts, err = h.Posts.ListBySpaceAndTag(r.Context(), sp.ID, tagFilter, pageSize, 0)
	} else {
		posts, err = h.Posts.ListBySpace(r.Context(), sp.ID, postType, pageSize, 0)
	}
	if err != nil {
		slog.Error("space page: list posts", "error", err)
		posts = nil
	}
	if posts == nil {
		posts = []model.Post{}
	}

	// Hydrate tags for each post so the post card can render them.
	// Intentionally one round trip per post - this is bounded by pageSize.
	if h.Tags != nil {
		for i := range posts {
			if tags, err := h.Tags.ListByPost(r.Context(), posts[i].ID); err == nil {
				posts[i].Tags = tags
			}
		}
	}

	// Sprint 14: attach reaction state in one batch query.
	if h.Reactions != nil {
		var viewerID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			viewerID = u.ID
		}
		if err := h.Reactions.AttachTo(r.Context(), viewerID, posts); err != nil {
			slog.Warn("space page: attach reactions", "error", err)
		}
	}
	// Sprint 15a B6: edited_at batched next to reactions.
	if h.EditHistory != nil {
		if err := h.EditHistory.AttachEditedAt(r.Context(), posts); err != nil {
			slog.Warn("space page: attach edited_at", "error", err)
		}
	}

	var popular []model.Tag
	if h.Tags != nil {
		popular, _ = h.Tags.ListBySpace(r.Context(), sp.ID, 25)
	}
	if popular == nil {
		popular = []model.Tag{}
	}

	data := h.newPageData(r, sp.Name+" - GoLab", sp.Slug)
	data["Content"] = map[string]any{
		"Space":       sp,
		"Posts":       posts,
		"PopularTags": popular,
		"ActiveType":  postType,
		"ActiveTag":   tagFilter,
	}
	if err := h.Render.Render(w, "space", data); err != nil {
		slog.Error("render space", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// List serves /api/spaces for the navigation bar and admin tools.
func (h *SpaceHandler) List(w http.ResponseWriter, r *http.Request) {
	spaces, err := h.Spaces.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if spaces == nil {
		spaces = []model.Space{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"spaces": spaces})
}

// TagPage renders /t/:slug with all posts carrying that tag.
type TagHandler struct {
	Render      *render.Engine
	Tags        *model.TagStore
	Posts       *model.PostStore
	Spaces      *model.SpaceStore // used by newPageData to populate the space bar
	Reactions   *model.ReactionStore
	EditHistory *model.PostEditHistoryStore // Sprint 15a B6
}

func (h *TagHandler) TagPage(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	tag, _ := h.Tags.FindBySlug(r.Context(), slug)
	if tag == nil {
		http.NotFound(w, r)
		return
	}

	posts, err := h.Posts.ListByTag(r.Context(), slug, 50, 0)
	if err != nil {
		slog.Error("tag page: list posts", "error", err)
		posts = nil
	}
	if posts == nil {
		posts = []model.Post{}
	}
	for i := range posts {
		if tags, err := h.Tags.ListByPost(r.Context(), posts[i].ID); err == nil {
			posts[i].Tags = tags
		}
	}
	if h.Reactions != nil {
		var viewerID int64
		if u := auth.UserFromContext(r.Context()); u != nil {
			viewerID = u.ID
		}
		if err := h.Reactions.AttachTo(r.Context(), viewerID, posts); err != nil {
			slog.Warn("tag page: attach reactions", "error", err)
		}
	}
	if h.EditHistory != nil {
		if err := h.EditHistory.AttachEditedAt(r.Context(), posts); err != nil {
			slog.Warn("tag page: attach edited_at", "error", err)
		}
	}

	data := h.newPageData(r, "#"+tag.Name+" - GoLab")
	data["Content"] = map[string]any{
		"Tag":   tag,
		"Posts": posts,
	}
	if err := h.Render.Render(w, "tag", data); err != nil {
		slog.Error("render tag", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// Search powers /api/tags/search?q=... for compose-box autocomplete.
// Returns max 10 tags, prefix-matched by slug.
func (h *TagHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	tags, err := h.Tags.Search(r.Context(), query, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if tags == nil {
		tags = []model.Tag{}
	}
	writeJSON(w, http.StatusOK, tags)
}

// newPageData mirrors the PageHandler's envelope. It populates the same
// keys (Spaces + CurrentSpace) that base.html's space-bar reads, so
// pages served by SpaceHandler and TagHandler render identically to
// pages served by PageHandler.
//
// The optional `spaces` store can be nil (tests, early boot) - the
// space bar then just renders empty.
func (h *SpaceHandler) newPageData(r *http.Request, title string, currentSlug string) map[string]any {
	data := map[string]any{
		"Title":        title,
		"SiteName":     "GoLab",
		"User":         auth.UserFromContext(r.Context()),
		"CurrentPath":  r.URL.Path,
		"CurrentSpace": currentSlug,
	}
	if h.Spaces != nil {
		if spaces, err := h.Spaces.List(r.Context()); err == nil {
			data["Spaces"] = spaces
		}
	}
	return data
}

// TagHandler's equivalent. Tags don't belong to a single space, so
// CurrentSpace is always empty here.
func (h *TagHandler) newPageData(r *http.Request, title string) map[string]any {
	data := map[string]any{
		"Title":        title,
		"SiteName":     "GoLab",
		"User":         auth.UserFromContext(r.Context()),
		"CurrentPath":  r.URL.Path,
		"CurrentSpace": "",
	}
	if h.Spaces != nil {
		if spaces, err := h.Spaces.List(r.Context()); err == nil {
			data["Spaces"] = spaces
		}
	}
	return data
}
