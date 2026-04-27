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
	Posts       *model.PostStore
	Channels    *model.ChannelStore
	Reactions   *model.ReactionStore
	Tags        *model.TagStore
	Spaces      *model.SpaceStore
	Users       *model.UserStore             // Sprint 14: resolve @username -> link on render
	Mentions    *model.MentionStore          // Sprint 14: record mentions on post create/update
	EditHistory *model.PostEditHistoryStore  // Sprint 15a B6: list / count edits
	Seasons     *model.SeasonStore           // Sprint 16b Phase 4: validate season_id assignment
	Projects    *model.ProjectStore          // Sprint 16b Phase 4: visibility check on season
	Markdown    *render.Markdown
	Sanitizer   *render.Sanitizer
	Hub         *Hub           // optional; when present, new posts get broadcast
	Notifs      *NotifDispatch // optional; used to create notifications on react/reply
}


type createPostRequest struct {
	Content   string   `json:"content"`
	ChannelID *int64   `json:"channel_id,omitempty"`
	ParentID  *int64   `json:"parent_id,omitempty"`
	SpaceID   *int64   `json:"space_id,omitempty"`
	SeasonID  *int64   `json:"season_id,omitempty"` // Sprint 16b: optional project season
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

// updatePostRequest is the body accepted by PATCH /api/posts/{id}.
// Only content is mutable for author self-edits; post_type, space,
// tags etc. stay fixed once created to keep the edit window simple
// and to match every other ActivityStreams-shaped platform.
type updatePostRequest struct {
	Content string `json:"content"`
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
	// Sprint 15a B8 Nit 4: reject the Quill-empty "<p><br></p>" and
	// similar tag-only bodies that pass the raw byte-length check.
	// The client's hasContent() gate catches this for the happy
	// path, but a direct curl bypasses it. See IsSemanticallyEmpty.
	if render.IsSemanticallyEmpty(req.Content) {
		writeError(w, http.StatusBadRequest, "content cannot be empty")
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

	// Sprint 16b: Validate season assignment. The season must exist,
	// must not be closed, and the user must have read access to the
	// project (otherwise we'd let users assign posts to seasons of
	// hidden projects they can't see).
	if req.SeasonID != nil {
		if h.Seasons == nil || h.Projects == nil {
			writeError(w, http.StatusBadRequest, "season assignment not available")
			return
		}
		season, err := h.Seasons.FindByID(r.Context(), *req.SeasonID)
		if err != nil {
			slog.Error("create post: find season", "id", *req.SeasonID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if season == nil {
			writeError(w, http.StatusBadRequest, "season not found")
			return
		}
		if season.Status == model.SeasonStatusClosed {
			writeError(w, http.StatusBadRequest, "season is closed; pick a different season")
			return
		}
		allowed, err := h.Projects.CanUserAccess(r.Context(), season.ProjectID, user.ID)
		if err != nil {
			slog.Error("create post: project access for season", "season", season.ID, "error", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		if !allowed {
			writeError(w, http.StatusBadRequest, "season not found")
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
		SeasonID:    req.SeasonID,
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

	// Broadcast to WebSocket subscribers. The broadcast is rendered
	// without a user context (see PublishNewPost commentary) so the
	// edit/delete dropdown gate does not leak to non-authors. For the
	// author's own client we render a second time WITH the user
	// context and return it inline so the .then() on the client can
	// prepend it immediately; the WS echo arriving after is deduped
	// by the self-echo guard in injectNewPost.
	var ownHTML string
	if h.Hub != nil {
		var slug string
		if req.ChannelID != nil {
			if ch, err := h.Channels.FindByID(r.Context(), *req.ChannelID); err == nil && ch != nil {
				slug = ch.Slug
			}
		}
		h.Hub.PublishNewPost(post, slug)
		ownHTML = h.Hub.RenderPostCard(post, user)
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"post": post,
		"html": ownHTML, // Sprint 15a B7 Bug 1: author-context fragment
	})
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

	// Sprint 15a B8 Nit 5: attach EditedAt so the single-post
	// endpoint is consistent with every other read path (feed,
	// channel, space, tag, profile, thread). Without this the
	// edit-post modal's openForPost path lost access to the edit
	// timestamp, and external API consumers saw a field that
	// randomly appeared on some endpoints and not others.
	if h.EditHistory != nil {
		if t, err := h.EditHistory.LastEditAt(r.Context(), post.ID); err == nil && !t.IsZero() {
			post.EditedAt = &t
		} else if err != nil {
			slog.Warn("get post: attach edited_at", "error", err, "post", post.ID)
		}
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

	// Pull the channel slug BEFORE deleting so we can broadcast to
	// the right WS room. Best-effort: if the lookup fails or the
	// post was never tied to a channel, we just skip the channel
	// topic and broadcast to "global" only.
	var channelSlug string
	if post, err := h.Posts.FindByID(r.Context(), id); err == nil && post != nil && post.ChannelID != nil {
		if ch, err := h.Channels.FindByID(r.Context(), *post.ChannelID); err == nil && ch != nil {
			channelSlug = ch.Slug
		}
	}

	if err := h.Posts.Delete(r.Context(), id, user.ID); err != nil {
		slog.Error("delete post", "error", err)
		writeError(w, http.StatusNotFound, "post not found or not owned by you")
		return
	}

	// Sprint 15a B5: fan out a post_deleted event so every open
	// feed removes the zombie card without a reload.
	if h.Hub != nil {
		h.Hub.PublishPostDeleted(id, channelSlug)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Update is the Sprint 15a B6 user-facing edit-post handler. Only
// the post's author may call it (admins get their own path in
// Sprint 15c via admin_moderation). The previous content is
// persisted to post_edit_history in the same transaction as the
// overwrite so we never lose the pre-edit text; that also drives
// the "edited" badge on the post card via a MAX(created_at)
// lookup.
func (h *PostHandler) Update(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid post id")
		return
	}

	var req updatePostRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Content) < 1 || len(req.Content) > 5000 {
		writeError(w, http.StatusBadRequest, "content must be 1-5000 characters")
		return
	}
	// Sprint 15a B8 Nit 4: mirror the Create check. "<p><br></p>"
	// and similar tag-only bodies would wipe the post to empty
	// server-side even though the client save path rejects them.
	if render.IsSemanticallyEmpty(req.Content) {
		writeError(w, http.StatusBadRequest, "content cannot be empty")
		return
	}

	existing, err := h.Posts.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("update post: find", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing == nil {
		writeError(w, http.StatusNotFound, "post not found")
		return
	}
	if existing.AuthorID != user.ID {
		// Admins don't use this path. Sprint 15c will add
		// /api/admin/posts/{id} with its own moderation_log hook.
		writeError(w, http.StatusForbidden, "only the author can edit this post")
		return
	}

	// Sanitise exactly like Create does so rich HTML from Quill or
	// Markdown from legacy clients both end up clean and mention-
	// linked before they hit the DB.
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

	updated, err := h.Posts.UpdateContent(r.Context(), model.UpdateContentParams{
		PostID:      id,
		EditorID:    user.ID,
		Content:     req.Content,
		ContentHTML: contentHTML,
		EditKind:    model.PostEditKindAuthor,
	})
	if err != nil {
		slog.Error("update post", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if updated == nil {
		// UpdateContent had the row a moment ago; a nil here means
		// it vanished between the snapshot and the re-read. Treat
		// as 404 rather than leaking a scary 500.
		writeError(w, http.StatusNotFound, "post disappeared during edit")
		return
	}

	// Re-mention hook: new mentions in the edited content should
	// notify, but existing ones should stay as-is (don't re-notify
	// on edit). MentionStore.SyncMentions handles the diff.
	if h.Mentions != nil {
		usernames := model.ExtractUsernames(req.Content)
		if added, err := h.Mentions.SyncMentions(r.Context(), id, usernames); err != nil {
			slog.Warn("update post: sync mentions", "post", id, "error", err)
		} else if h.Notifs != nil {
			pid := id
			for _, uid := range added {
				if uid == user.ID {
					continue
				}
				h.Notifs.Notify(r.Context(), uid, user.ID, model.NotifMention, &pid)
			}
		}
	}

	// Populate the EditedAt field so the fragment re-render picks
	// up the "edited" badge without a second roundtrip.
	if h.EditHistory != nil {
		if t, err := h.EditHistory.LastEditAt(r.Context(), id); err == nil && !t.IsZero() {
			updated.EditedAt = &t
		}
	}

	slog.Info("post edited", "id", id, "author", user.Username)

	// Sprint 15a B7 Bug 3: broadcast the edit so any open tab /
	// device viewing this post patches its content block in place
	// instead of showing the stale text until reload. The payload
	// is structured (id + content_html + edited_at) not a full HTML
	// fragment - the target card already exists in every viewer's
	// DOM, so we only need the content delta, which sidesteps the
	// user-context dropdown issue PublishNewPost has to solve with
	// a second render.
	if h.Hub != nil {
		var slug string
		if updated.ChannelID != nil {
			if ch, err := h.Channels.FindByID(r.Context(), *updated.ChannelID); err == nil && ch != nil {
				slug = ch.Slug
			}
		}
		h.Hub.PublishPostUpdated(updated, slug)
	}

	writeJSON(w, http.StatusOK, map[string]any{"post": updated})
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
