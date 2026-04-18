package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

type ProfileHandler struct {
	Users    *model.UserStore
	Posts    *model.PostStore
	Follows  *model.FollowStore
	Sessions *auth.SessionStore
	Settings *model.SettingsStore
	Notifs   *NotifDispatch
}

type updateProfileRequest struct {
	DisplayName string `json:"display_name"`
	Bio         string `json:"bio"`
	AvatarURL   string `json:"avatar_url"`
	Username    string `json:"username"`
}

// decodeUpdateProfile reads from JSON or form body.
func decodeUpdateProfile(r *http.Request) (updateProfileRequest, error) {
	var req updateProfileRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.DisplayName = r.Form.Get("display_name")
		req.Bio = r.Form.Get("bio")
		req.AvatarURL = r.Form.Get("avatar_url")
		req.Username = r.Form.Get("username")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// isValidUsername mirrors the registration rule: 3-32 chars, only
// [a-zA-Z0-9_]. Shared by settings, admin and client-side validation.
func isValidUsername(username string) bool {
	if len(username) < 3 || len(username) > 32 {
		return false
	}
	for _, c := range username {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '_':
		default:
			return false
		}
	}
	return true
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

	req, err := decodeUpdateProfile(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.DisplayName) > 64 {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "display name must be 64 characters or fewer")
		return
	}

	// Username change path (Sprint 13). Only triggers if the user actually
	// submitted a new value different from the current handle. An empty
	// Username from the JSON/form body means "don't touch it".
	req.Username = strings.TrimSpace(req.Username)
	if req.Username != "" && req.Username != user.Username {
		// Admins bypass the platform toggle; everyone else is gated by
		// the allow_username_change setting so owners can shut it off.
		if user.PowerLevel < 75 {
			allowed := true
			if h.Settings != nil {
				allowed = h.Settings.GetBool(r.Context(), "allow_username_change")
			}
			if !allowed {
				errorRedirectOrJSON(w, r, "/settings", http.StatusForbidden, "username changes are currently disabled")
				return
			}
		}
		if !isValidUsername(req.Username) {
			errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "username must be 3-32 alphanumeric characters or underscores")
			return
		}
		// Case-insensitive uniqueness check so we reject "Prinz" when
		// "prinz" already exists. Only fail when the match is NOT the
		// current user (changing case of own handle is allowed).
		taken, err := h.Users.UsernameExists(r.Context(), req.Username)
		if err != nil {
			slog.Error("update profile: check username", "error", err)
			errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
			return
		}
		if taken && !strings.EqualFold(req.Username, user.Username) {
			errorRedirectOrJSON(w, r, "/settings", http.StatusConflict, "username already taken")
			return
		}
		if err := h.Users.UpdateUsername(r.Context(), user.ID, req.Username); err != nil {
			slog.Error("update username", "error", err)
			errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
			return
		}
		slog.Info("username changed", "user_id", user.ID, "old", user.Username, "new", req.Username)
	}

	if err := h.Users.UpdateProfile(r.Context(), user.ID, req.DisplayName, req.Bio, req.AvatarURL); err != nil {
		slog.Error("update profile", "error", err)
		errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
		return
	}

	redirectOrJSON(w, r, "/settings", map[string]string{"status": "updated"})
}

// ---------- Password change (Sprint 13) ----------

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func decodeChangePassword(r *http.Request) (changePasswordRequest, error) {
	var req changePasswordRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.CurrentPassword = r.Form.Get("current_password")
		req.NewPassword = r.Form.Get("new_password")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// ChangePassword verifies the caller's current password and swaps in a
// new bcrypt hash. On success, ALL sessions for the user are revoked
// (including the current one) and the cookie is cleared, so every
// device is forced to log in again. The no-JS form path redirects to
// /login?msg=password-changed so the page can show a success banner.
func (h *ProfileHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	req, err := decodeChangePassword(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "invalid request body")
		return
	}

	if req.CurrentPassword == "" || req.NewPassword == "" {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "current and new password are required")
		return
	}

	// Reload from DB so we have the freshest hash - context user might
	// be a cached copy from middleware.
	full, err := h.Users.FindByID(r.Context(), user.ID)
	if err != nil || full == nil {
		slog.Error("change password: find user", "error", err)
		errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
		return
	}

	if !auth.CheckPassword(full.PasswordHash, req.CurrentPassword) {
		errorRedirectOrJSON(w, r, "/settings", http.StatusUnauthorized, "current password is incorrect")
		return
	}

	// NIST SP 800-63B: length only, 8-128 characters. Matches register.
	if len(req.NewPassword) < 8 {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "new password must be at least 8 characters")
		return
	}
	if len(req.NewPassword) > 128 {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "new password must not exceed 128 characters")
		return
	}
	if req.NewPassword == req.CurrentPassword {
		errorRedirectOrJSON(w, r, "/settings", http.StatusBadRequest, "new password must differ from current password")
		return
	}

	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		slog.Error("change password: hash", "error", err)
		errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
		return
	}

	if err := h.Users.UpdatePassword(r.Context(), user.ID, newHash); err != nil {
		slog.Error("change password: update", "error", err)
		errorRedirectOrJSON(w, r, "/settings", http.StatusInternalServerError, "internal error")
		return
	}

	// Revoke every active session for this user (this device too) so
	// a leaked session cookie becomes useless the moment the real
	// owner changes their password.
	if h.Sessions != nil {
		if err := h.Sessions.DeleteAllForUser(r.Context(), user.ID); err != nil {
			slog.Warn("change password: delete sessions", "error", err)
		}
	}

	// Clear cookie under both possible names so the browser drops it
	// whichever prefix the deployment is currently using.
	for _, name := range []string{"__Host-golab_session", "session_id"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}

	slog.Info("password changed", "user_id", user.ID)

	if wantsFormResponse(r) {
		http.Redirect(w, r, "/login?msg=password-changed", http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "password-changed"})
}

// ---------- Username availability (Sprint 13) ----------

// CheckUsername is a live-validation probe for the settings form.
// Returns {"available": bool, "reason": "..."}. The reason field is
// populated when available is false so the UI can show something
// specific ("invalid format", "already taken", "same as current").
func (h *ProfileHandler) CheckUsername(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	username := strings.TrimSpace(r.URL.Query().Get("username"))
	if username == "" {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "empty"})
		return
	}
	if !isValidUsername(username) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "invalid"})
		return
	}
	// Treat "same as current" as not-available so the submit stays
	// idle. The UI can word this as "this is already your username".
	if strings.EqualFold(username, user.Username) {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "same"})
		return
	}
	exists, err := h.Users.UsernameExists(r.Context(), username)
	if err != nil {
		slog.Error("check username", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if exists {
		writeJSON(w, http.StatusOK, map[string]any{"available": false, "reason": "taken"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"available": true})
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
