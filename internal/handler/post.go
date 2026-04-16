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
	Markdown  *render.Markdown
	Hub       *Hub           // optional; when present, new posts get broadcast
	Notifs    *NotifDispatch // optional; used to create notifications on react/reply
}

type createPostRequest struct {
	Content   string `json:"content"`
	ChannelID *int64 `json:"channel_id,omitempty"`
	ParentID  *int64 `json:"parent_id,omitempty"`
}

type reactRequest struct {
	ReactionType string `json:"reaction_type"`
}

type previewRequest struct {
	Content string `json:"content"`
}

// Preview renders Markdown without saving. Used by the compose box
// Preview tab. Rate-limited by the auth middleware.
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
	if h.Markdown != nil {
		html, _ = h.Markdown.Render(req.Content)
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

	// Render Markdown to HTML (XSS-safe: goldmark escapes raw HTML).
	var contentHTML string
	if h.Markdown != nil {
		if rendered, err := h.Markdown.Render(req.Content); err == nil {
			contentHTML = rendered
		}
	}

	post, err := h.Posts.Create(r.Context(), "Note", user.ID, req.ChannelID, req.ParentID, req.Content, contentHTML)
	if err != nil {
		slog.Error("create post", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Populate author fields for fragment rendering
	post.AuthorUsername = user.Username
	post.AuthorDisplayName = user.DisplayName
	post.AuthorAvatarURL = user.AvatarURL

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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ReactionType == "" {
		req.ReactionType = "like"
	}

	if err := h.Reactions.React(r.Context(), user.ID, id, req.ReactionType); err != nil {
		slog.Error("react", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Notify the post author (unless the user is reacting to their own post).
	if h.Notifs != nil {
		if post, err := h.Posts.FindByID(r.Context(), id); err == nil && post != nil {
			h.Notifs.Notify(r.Context(), post.AuthorID, user.ID, model.NotifReaction, &id)
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "reacted"})
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
