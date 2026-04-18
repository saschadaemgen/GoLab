package handler

import (
	"context"
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
	Users     *model.UserStore     // Sprint 14: resolve @username -> link on render
	Mentions  *model.MentionStore  // Sprint 14: record mentions on post create/update
	Markdown  *render.Markdown
	Sanitizer *render.Sanitizer
	Hub       *Hub           // optional; when present, new posts get broadcast
	Notifs    *NotifDispatch // optional; used to create notifications on react/reply
}


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
// else falls back to "discussion". The set now includes "announcement"
// because announcements cross every thematic space - they're an axis
// on posts, not a space of their own (Sprint 10.5 revision).
var validPostTypes = map[string]bool{
	"discussion":   true,
	"question":     true,
	"tutorial":     true,
	"code":         true,
	"showcase":     true,
	"link":         true,
	"announcement": true,
}

// minPowerToPostAnnouncement guards the announcement post type. Only
// users at or above this level can mark a post as an announcement,
// regardless of which space it lives in. Matches the same threshold
// the UI enforces client-side.
const minPowerToPostAnnouncement = 75

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

	// Announcement post type is admin-only (power_level >= 75),
	// regardless of which space the post lives in.
	if req.PostType == "announcement" && user.PowerLevel < minPowerToPostAnnouncement {
		writeError(w, http.StatusForbidden, "only admins can publish announcements")
		return
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
	//
	// Sprint 14: after sanitizing we post-process the HTML to turn
	// `@username` tokens into profile links. Done last so a raw
	// "@admin" literal stored by an old Markdown client still
	// becomes a live link on display.
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
	if h.Users != nil {
		contentHTML = render.LinkMentions(contentHTML, h.mentionResolver(r.Context()))
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

	// Sprint 14: record @mentions and fan out notifications. Best-
	// effort: the post is already saved, a mention failure should
	// not 500 the response. Self-mentions don't notify.
	if h.Mentions != nil {
		usernames := model.ExtractUsernames(req.Content)
		if len(usernames) > 0 {
			userIDs, err := h.Mentions.RecordMentions(r.Context(), post.ID, usernames)
			if err != nil {
				slog.Warn("create post: record mentions", "post", post.ID, "error", err)
			}
			if h.Notifs != nil {
				pid := post.ID
				for _, uid := range userIDs {
					if uid == user.ID {
						continue
					}
					h.Notifs.Notify(r.Context(), uid, user.ID, model.NotifMention, &pid)
				}
			}
		}
	}

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

// React toggles one (post, user, emoji) triple and returns the
// full reaction state afterwards. Sprint 14 shape:
//
//	{
//	  "result": "added" | "removed",
//	  "user_types": ["heart", "thumbsup"],
//	  "counts": { "heart": 3, "thumbsup": 1, "laugh": 0, ... }
//	}
//
// The 6 keys in "counts" are always present, zero when no rows.
// Clients render all 6 chips and highlight the ones listed in
// "user_types". A user adding a second distinct emoji counts as
// ReactAdded and fires a fresh notification - that matches the
// GitHub multi-reaction behaviour.
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

	// Notify on every add (including second-emoji-on-same-post),
	// never on remove. The NotificationStore already dedupes
	// within 60s so fast add/remove/add can't spam the author.
	if h.Notifs != nil && result == model.ReactAdded {
		if post, err := h.Posts.FindByID(r.Context(), id); err == nil && post != nil {
			h.Notifs.Notify(r.Context(), post.AuthorID, user.ID, model.NotifReaction, &id)
		}
	}

	state, err := h.Reactions.StateFor(r.Context(), user.ID, id)
	if err != nil {
		slog.Error("react: state", "error", err)
		// The toggle succeeded; return what we can.
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"result":     string(result),
		"user_types": state.UserTypes,
		"counts":     state.Counts,
	})
}

// Unreact is kept as a fallback for clients that predate Sprint 14.
// It wipes EVERY reaction the user holds on the post. Logged at
// Warn so we can track remaining legacy callers from container
// logs.
func (h *PostHandler) Unreact(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	slog.Warn("legacy unreact endpoint hit",
		"user_id", user.ID, "post_id", id,
		"user_agent", r.Header.Get("User-Agent"))

	if err := h.Reactions.Unreact(r.Context(), user.ID, id); err != nil {
		slog.Error("unreact", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unreacted"})
}

// mentionResolver is the closure passed to render.LinkMentions.
// It memoises lookups per invocation so a post that mentions the
// same username three times only queries the DB once. Returns
// false for unknown / banned / pending users, and LinkMentions
// then leaves the raw "@username" text unchanged.
func (h *PostHandler) mentionResolver(ctx context.Context) func(username string) (int64, bool) {
	cache := make(map[string]int64)
	return func(username string) (int64, bool) {
		if h.Users == nil {
			return 0, false
		}
		if id, ok := cache[username]; ok {
			return id, id > 0
		}
		u, err := h.Users.FindByUsername(ctx, username)
		if err != nil || u == nil || u.Status != model.UserStatusActive {
			cache[username] = 0
			return 0, false
		}
		cache[username] = u.ID
		return u.ID, true
	}
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
