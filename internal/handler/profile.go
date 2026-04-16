package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

type ProfileHandler struct {
	Users   *model.UserStore
	Posts   *model.PostStore
	Follows *model.FollowStore
	Notifs  *NotifDispatch
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURL   string `json:"avatar_url"`
}

func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	username := chi.URLParam(r, "username")

	user, err := h.Users.FindByUsername(r.Context(), username)
	if err != nil {
		slog.Error("get profile", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	// Get counts
	followerCount, _ := h.Follows.FollowerCount(r.Context(), user.ID)
	followingCount, _ := h.Follows.FollowingCount(r.Context(), user.ID)

	// Get recent posts
	recentPosts, err := h.Posts.ListByAuthor(r.Context(), user.ID, 10, nil)
	if err != nil {
		slog.Error("get profile: list posts", "error", err)
		recentPosts = []model.Post{}
	}
	if recentPosts == nil {
		recentPosts = []model.Post{}
	}

	// Check if current user is following
	isFollowing := false
	currentUser := auth.UserFromContext(r.Context())
	if currentUser != nil && currentUser.ID != user.ID {
		isFollowing, _ = h.Follows.IsFollowing(r.Context(), currentUser.ID, user.ID)
	}

	// Clear email for non-self profiles
	if currentUser == nil || currentUser.ID != user.ID {
		user.Email = ""
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"user":            user,
		"follower_count":  followerCount,
		"following_count": followingCount,
		"recent_posts":    recentPosts,
		"is_following":    isFollowing,
	})
}

func (h *ProfileHandler) UpdateMe(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.DisplayName) > 64 {
		writeError(w, http.StatusBadRequest, "display name must be 64 characters or fewer")
		return
	}

	if err := h.Users.UpdateProfile(r.Context(), user.ID, req.DisplayName, req.Bio, req.AvatarURL); err != nil {
		slog.Error("update profile", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (h *ProfileHandler) Follow(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.UserFromContext(r.Context())
	username := chi.URLParam(r, "username")

	target, err := h.Users.FindByUsername(r.Context(), username)
	if err != nil {
		slog.Error("follow: find user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if currentUser.ID == target.ID {
		writeError(w, http.StatusBadRequest, "cannot follow yourself")
		return
	}

	if err := h.Follows.Follow(r.Context(), currentUser.ID, target.ID); err != nil {
		slog.Error("follow", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if h.Notifs != nil {
		h.Notifs.Notify(r.Context(), target.ID, currentUser.ID, model.NotifFollow, nil)
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "following"})
}

func (h *ProfileHandler) Unfollow(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.UserFromContext(r.Context())
	username := chi.URLParam(r, "username")

	target, err := h.Users.FindByUsername(r.Context(), username)
	if err != nil {
		slog.Error("unfollow: find user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}

	if err := h.Follows.Unfollow(r.Context(), currentUser.ID, target.ID); err != nil {
		slog.Error("unfollow", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "unfollowed"})
}
