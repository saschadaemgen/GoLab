package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

type PostHandler struct {
	Posts     *model.PostStore
	Channels  *model.ChannelStore
	Reactions *model.ReactionStore
	Tags      *model.TagStore
	Spaces    *model.SpaceStore
	Markdown  *render.Markdown
	Sanitizer *render.Sanitizer
	Hub       *Hub           // optional; when present, new posts get broadcast
	Notifs    *NotifDispatch // optional; used to create notifications on react/reply
}

// minPowerToPostInAnnouncements is the admin threshold. Only users at
// or above this level can post to the "announcements" space. Sprint 10.5
// rule.
const minPowerToPostInAnnouncements = 75

type createPostRequest struct {
	Content   string   `json:"content"`
	ChannelID *int64   `json:"channel_id,omitempty"`
	ParentID  *int64   `json:"parent_id,omitempty"`
	SpaceID   *int64   `json:"space_id,omitempty"`
	PostType  string   `json:"post_type,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// maxTagsPerPost enforces the briefing rule: at most 5 tags. We also
// enforce it client-side in the Alpine tagInput component.
const maxTagsPerPost = 5

// validPostTypes are the post_type values the server accepts. Anything
// else falls back to "discussion".
var validPostTypes = map[string]bool{
	"discussion": true,
	"question":   true,
	"tutorial":   true,
	"code":       true,
	"showcase":   true,
	"link":       true,
}

type reactRequest struct {
	ReactionType string `json:"reaction_type"`
}

// allowedReactionTypes gates the 6 Sprint 10.5 emoji reactions.
// Anything else falls back to "heart".
var allowedReactionTypes = map[string]bool{
	"heart":     true,
	"thumbsup":  true,
	"laugh":     true,
	"surprised": true,
	"sad":       true,
	"fire":      true,
}

type previewRequest struct {
	Content string `json:"content"`
}

// Preview renders Markdown or sanitizes Quill HTML without saving.
func (h *PostHandler) Preview(w http.ResponseWriter, r *http.Request) {
	var req previewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Content) > 5000 {
		writeError(w, http.StatusBadRequest, "content too long")
		return
	}
	var html string
	if render.LooksLikeHTML(req.Content) {
		if h.Sanitizer != nil {
			html = h.Sanitizer.Clean(req.Content)
		} else {
			html = req.Content
		}
	} else if h.Markdown != nil {
		html, _ = h.Markdown.Render(req.Content)
		if h.Sanitizer != nil {
			html = h.Sanitizer.Clean(html)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": html})
}

func (h *PostHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var req createPostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.Content) < 1 || len(req.Content) > 5000 {
		writeError(w, http.StatusBadRequest, "content must be 1-5000 characters")
		return
	}

	// Validate channel membership if posting to a channel
	if req.ChannelID != nil {
		isMember, err := h.Channels.IsMember(r.Context(), *req.ChannelID, user.ID)
		if err != nil {
			slog.Error("create post: check membership", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !isMember {
			writeError(w, http.StatusForbidden, "must be a channel member to post")
			return
		}
	}

	// Announcements space: admin-only posting (power_level >= 75).
	// Look up the space by ID; if the client picked announcements and
	// the user doesn't have the power, reject.
	if req.SpaceID != nil && h.Spaces != nil {
		if sp, err := h.Spaces.FindByID(r.Context(), *req.SpaceID); err == nil && sp != nil {
			if sp.Slug == "announcements" && user.PowerLevel < minPowerToPostInAnnouncements {
				writeError(w, http.StatusForbidden, "only admins can post to Announcements")
				return
			}
		}
	}

	// Validate parent exists if replying
	if req.ParentID != nil {
		parent, err := h.Posts.FindByID(r.Context(), *req.ParentID)
		if err != nil {
			slog.Error("create post: find parent", "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if parent == nil {
			writeError(w, http.StatusNotFound, "parent post not found")
			return
		}
	}

	// Dual-format support:
	//
	// - If the content looks like HTML (Quill output from the rich editor),
	//   sanitize with bluemonday and store the cleaned HTML in content_html.
	//   The `content` column still holds the raw HTML as-submitted so we can
	//   re-sanitize later without losing fidelity.
	//
	// - Otherwise treat the content as Markdown / plain text and run goldmark.
	//   The rendered HTML is already XSS-safe (goldmark escapes raw HTML by
	//   default), but we pass it through the sanitizer too as defense-in-depth.
	//
	// This lets the new rich editor and old Markdown clients coexist with a
	// single `content_html` column used for display.
	var contentHTML string
	if render.LooksLikeHTML(req.Content) {
		if h.Sanitizer != nil {
			contentHTML = h.Sanitizer.Clean(req.Content)
		} else {
			contentHTML = req.Content
		}
	} else if h.Markdown != nil {
		if rendered, err := h.Markdown.Render(req.Content); err == nil {
			if h.Sanitizer != nil {
				contentHTML = h.Sanitizer.Clean(rendered)
			} else {
				contentHTML = rendered
			}
		}
	}

	// Normalise post_type to a known value. Empty / unknown -> discussion.
	postType := req.PostType
	if !validPostTypes[postType] {
		postType = "discussion"
	}

	post, err := h.Posts.Create(r.Context(), model.CreateParams{
		ASType:      "Note",
		AuthorID:    user.ID,
		ChannelID:   req.ChannelID,
		ParentID:    req.ParentID,
		SpaceID:     req.SpaceID,
		PostType:    postType,
		Content:     req.Content,
		ContentHTML: contentHTML,
	})
	if err != nil {
		slog.Error("create post", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Populate author fields for fragment rendering. FindByID inside
	// Create already filled the joined user fields, but a reply in a
	// fragment should still see the current user's avatar without
	// re-querying. Harmless overwrite with identical data.
	post.AuthorUsername = user.Username
	post.AuthorDisplayName = user.DisplayName
	post.AuthorAvatarURL = user.AvatarURL

	// Attach tags. Each tag name is sluggified, FindOrCreate'd, then
	// joined to the post. Duplicates inside the request are collapsed by
	// the DB unique constraint. Cap at maxTagsPerPost so a malicious
	// client can't spam 5000 tags.
	if h.Tags != nil && len(req.Tags) > 0 {
		tagIDs := make([]int64, 0, len(req.Tags))
		seen := map[string]bool{}
		for i, name := range req.Tags {
			if i >= maxTagsPerPost {
				break
			}
			slug := model.Slugify(name)
			if slug == "" || seen[slug] {
				continue
			}
			seen[slug] = true
			tag, err := h.Tags.FindOrCreate(r.Context(), slug, user.ID)
			if err != nil {
				slog.Warn("create post: find or create tag", "tag", slug, "error", err)
				continue
			}
			tagIDs = append(tagIDs, tag.ID)
		}
		if err := h.Tags.AttachToPost(r.Context(), post.ID, tagIDs); err != nil {
			slog.Warn("create post: attach tags", "error", err)
		}
		// Reload with tag data for the fragment render downstream.
		if tags, err := h.Tags.ListByPost(r.Context(), post.ID); err == nil {
			post.Tags = tags
		}
	}

	slog.Info("post created", "id", post.ID, "author", user.Username)

	// Notify the parent post author if this is a reply
	if req.ParentID != nil && h.Notifs != nil {
		if parent, err := h.Posts.FindByID(r.Context(), *req.ParentID); err == nil && parent != nil {
			pid := post.ID
			h.Notifs.Notify(r.Context(), parent.AuthorID, user.ID, model.NotifReply, &pid)
		}
	}

	// Broadcast to WebSocket subscribers
	if h.Hub != nil {
		var slug string
		if req.ChannelID != nil {
			if ch, err := h.Channels.FindByID(r.Context(), *req.ChannelID); err == nil && ch != nil {
				slug = ch.Slug
			}
		}
		h.Hub.PublishNewPost(post, slug)
	}

	writeJSON(w, http.StatusCreated, map[string]any{"post": post})
}

func (h *PostHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	post, err := h.Posts.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("get post", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if post == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}

	// Check if current user has reacted
	user := auth.UserFromContext(r.Context())
	hasReacted := false
	if user != nil {
		hasReacted, _ = h.Reactions.HasReacted(r.Context(), user.ID, post.ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"post":        post,
		"has_reacted": hasReacted,
	})
}

func (h *PostHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	if err := h.Posts.Delete(r.Context(), id, user.ID); err != nil {
		slog.Error("delete post", "error", err)
		writeError(w, http.StatusNotFound, "post not found or not owned by you")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *PostHandler) React(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	var req reactRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // body is optional
	if !allowedReactionTypes[req.ReactionType] {
		req.ReactionType = "heart"
	}

	result, err := h.Reactions.Toggle(r.Context(), user.ID, id, req.ReactionType)
	if err != nil {
		slog.Error("react", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Only fire a notification when a reaction was added or switched -
	// removing a reaction shouldn't re-notify the post author.
	if h.Notifs != nil && result != model.ReactRemoved {
		if post, err := h.Posts.FindByID(r.Context(), id); err == nil && post != nil {
			h.Notifs.Notify(r.Context(), post.AuthorID, user.ID, model.NotifReaction, &id)
		}
	}

	// Return the final state so the client can update without a reload:
	// which type (if any) this user now holds, plus the total count.
	userType, _ := h.Reactions.UserReactionType(r.Context(), user.ID, id)
	var count int
	if p, err := h.Posts.FindByID(r.Context(), id); err == nil && p != nil {
		count = p.ReactionCount
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result":    string(result),
		"user_type": userType,
		"count":     count,
	})
}

func (h *PostHandler) Unreact(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	if err := h.Reactions.Unreact(r.Context(), user.ID, id); err != nil {
		slog.Error("unreact", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unreacted"})
}

func (h *PostHandler) Repost(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	// Verify original post exists
	original, err := h.Posts.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("repost: find original", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if original == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}

	post, err := h.Posts.CreateRepost(r.Context(), user.ID, nil, id)
	if err != nil {
		slog.Error("repost", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"post": post})
}
