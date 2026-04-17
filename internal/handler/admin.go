package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
	"github.com/saschadaemgen/GoLab/internal/render"
)

// RequireAdmin blocks users whose power_level is below 75. Admins
// (75) handle moderation (approve/reject, ban/unban). Sensitive
// mutations like changing platform settings are further restricted
// inside individual handlers (e.g. SetSetting requires id=1).
//
// Place this middleware after RequireAuth (or RequireAuthRedirect
// for HTML pages).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil || u.PowerLevel < 75 {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminHandler serves the /admin dashboard and its API companions.
type AdminHandler struct {
	DB            *pgxpool.Pool
	Render        *render.Engine
	Users         *model.UserStore
	Settings      *model.SettingsStore
	Notifications *model.NotificationStore
	Hub           *Hub
}

type adminStats struct {
	Users    int `json:"users"`
	Posts    int `json:"posts"`
	Channels int `json:"channels"`
	Banned   int `json:"banned"`
}

type adminUser struct {
	ID          int64     `json:"id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	PowerLevel  int       `json:"power_level"`
	PostCount   int       `json:"post_count"`
	Banned      bool      `json:"banned"`
	CreatedAt   time.Time `json:"created_at"`
}

type adminChannel struct {
	ID          int64     `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	ChannelType string    `json:"channel_type"`
	MemberCount int       `json:"member_count"`
	PostCount   int       `json:"post_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type adminDashboard struct {
	Stats           adminStats
	Users           []adminUser
	Channels        []adminChannel
	PendingUsers    []model.User // Sprint 12 moderation queue
	RequireApproval bool         // current state of the toggle
}

// Page renders the full /admin dashboard (server-rendered).
func (h *AdminHandler) Page(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		"Title":       "Admin - GoLab",
		"SiteName":    "GoLab",
		"User":        auth.UserFromContext(r.Context()),
		"CurrentPath": r.URL.Path,
		"Content":     h.collect(r),
	}
	if err := h.Render.Render(w, "admin", data); err != nil {
		slog.Error("render admin", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func (h *AdminHandler) collect(r *http.Request) adminDashboard {
	out := adminDashboard{
		Users:    []adminUser{},
		Channels: []adminChannel{},
	}
	ctx := r.Context()

	// Stats
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&out.Stats.Users)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM posts`).Scan(&out.Stats.Posts)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM channels`).Scan(&out.Stats.Channels)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users WHERE banned`).Scan(&out.Stats.Banned)

	// Users
	rows, err := h.DB.Query(ctx, `
		SELECT u.id, u.username, u.display_name, u.power_level, u.banned, u.created_at,
		       (SELECT COUNT(*) FROM posts p WHERE p.author_id = u.id) AS posts
		FROM users u
		ORDER BY u.created_at DESC
		LIMIT 50`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var au adminUser
			if err := rows.Scan(&au.ID, &au.Username, &au.DisplayName, &au.PowerLevel, &au.Banned, &au.CreatedAt, &au.PostCount); err == nil {
				out.Users = append(out.Users, au)
			}
		}
	}

	// Channels
	crows, err := h.DB.Query(ctx, `
		SELECT c.id, c.slug, c.name, c.channel_type, c.member_count, c.created_at,
		       (SELECT COUNT(*) FROM posts p WHERE p.channel_id = c.id) AS posts
		FROM channels c
		ORDER BY c.created_at DESC
		LIMIT 50`)
	if err == nil {
		defer crows.Close()
		for crows.Next() {
			var ac adminChannel
			if err := crows.Scan(&ac.ID, &ac.Slug, &ac.Name, &ac.ChannelType, &ac.MemberCount, &ac.CreatedAt, &ac.PostCount); err == nil {
				out.Channels = append(out.Channels, ac)
			}
		}
	}

	// Sprint 12 moderation data: pending users queue + require_approval
	// setting current state for the toggle.
	if h.Users != nil {
		if pending, err := h.Users.ListPending(ctx, 50); err == nil {
			out.PendingUsers = pending
		}
	}
	if h.Settings != nil {
		out.RequireApproval = h.Settings.GetBool(ctx, "require_approval")
	}
	return out
}

// ---- API endpoints ----

func (h *AdminHandler) Stats(w http.ResponseWriter, r *http.Request) {
	d := h.collect(r)
	writeJSON(w, http.StatusOK, d.Stats)
}

func (h *AdminHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	d := h.collect(r)
	writeJSON(w, http.StatusOK, map[string]any{"users": d.Users})
}

func (h *AdminHandler) Ban(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	actor := auth.UserFromContext(r.Context())
	if actor != nil && actor.ID == id {
		writeError(w, http.StatusBadRequest, "cannot ban yourself")
		return
	}
	_, err = h.DB.Exec(r.Context(),
		`UPDATE users SET banned = TRUE, banned_at = NOW(), banned_reason = $2 WHERE id = $1`,
		id, "admin action")
	if err != nil {
		slog.Error("ban", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Also revoke any active sessions for that user.
	_, _ = h.DB.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "banned"})
}

func (h *AdminHandler) Unban(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	_, err = h.DB.Exec(r.Context(),
		`UPDATE users SET banned = FALSE, banned_at = NULL, banned_reason = '' WHERE id = $1`, id)
	if err != nil {
		slog.Error("unban", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unbanned"})
}

type powerReq struct {
	PowerLevel int `json:"power_level"`
}

// Approve a pending user. Sets status = active and dispatches an
// "approved" notification to the user. Safe to call on an already-
// active user (it's a no-op UPDATE that still touches updated_at).
func (h *AdminHandler) Approve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	actor := auth.UserFromContext(r.Context())
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.Users.SetStatus(r.Context(), id, model.UserStatusActive, actor.ID); err != nil {
		slog.Error("approve user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Tell the approved user.
	if h.Notifications != nil {
		if _, err := h.Notifications.Create(r.Context(), id, actor.ID, model.NotifApproved, nil); err == nil && h.Hub != nil {
			count, _ := h.Notifications.UnreadCount(r.Context(), id)
			h.Hub.PublishToUser(id, Message{
				Type: "notification_count",
				Data: map[string]int{"count": count},
			})
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// Reject a pending user. Sets status = rejected and notifies them.
// The user can still log in but every mutation path is blocked by
// RequireActiveUser, so effectively they see a read-only ghost.
func (h *AdminHandler) Reject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	actor := auth.UserFromContext(r.Context())
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if err := h.Users.SetStatus(r.Context(), id, model.UserStatusRejected, actor.ID); err != nil {
		slog.Error("reject user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	// Kill any active sessions of the rejected user so they're logged
	// out immediately.
	_, _ = h.DB.Exec(r.Context(), `DELETE FROM sessions WHERE user_id = $1`, id)

	if h.Notifications != nil {
		_, _ = h.Notifications.Create(r.Context(), id, actor.ID, model.NotifRejected, nil)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "rejected"})
}

// Pending returns the list of users waiting for approval. JSON for
// the Alpine refresh on the admin dashboard.
func (h *AdminHandler) Pending(w http.ResponseWriter, r *http.Request) {
	if h.Users == nil {
		writeJSON(w, http.StatusOK, map[string]any{"users": []any{}})
		return
	}
	users, err := h.Users.ListPending(r.Context(), 50)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if users == nil {
		users = []model.User{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// SetSetting updates a platform-level setting. Whitelisted to a tiny
// set of known keys so the endpoint can't be abused to write arbitrary
// rows. Only the Owner (id=1) can change settings.
type setSettingReq struct {
	Value string `json:"value"`
}

var allowedSettings = map[string]bool{
	"require_approval": true,
}

func (h *AdminHandler) SetSetting(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFromContext(r.Context())
	if actor == nil || actor.ID != 1 {
		writeError(w, http.StatusForbidden, "only the platform owner can change settings")
		return
	}
	key := chi.URLParam(r, "key")
	if !allowedSettings[key] {
		writeError(w, http.StatusBadRequest, "unknown setting")
		return
	}
	var req setSettingReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if h.Settings == nil {
		writeError(w, http.StatusInternalServerError, "settings unavailable")
		return
	}
	if err := h.Settings.Set(r.Context(), key, req.Value); err != nil {
		slog.Error("set setting", "error", err, "key", key)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": req.Value})
}

func (h *AdminHandler) SetPower(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	var req powerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if req.PowerLevel < 0 || req.PowerLevel > 100 {
		writeError(w, http.StatusBadRequest, "power level out of range")
		return
	}

	// Protection rules (server-enforced, never trust client):
	//   1) You cannot change your own power level (prevents self-demote lock-out
	//      or self-promote if you were somehow set admin by a script).
	//   2) You cannot assign a power level higher than your own. Only user id=1
	//      is allowed to create other Owners (100).
	actor := auth.UserFromContext(r.Context())
	if actor == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if actor.ID == id {
		writeError(w, http.StatusForbidden, "cannot change your own power level")
		return
	}
	if req.PowerLevel > actor.PowerLevel {
		writeError(w, http.StatusForbidden, "cannot assign a power level higher than your own")
		return
	}
	if req.PowerLevel == 100 && actor.ID != 1 {
		writeError(w, http.StatusForbidden, "only the platform owner (id=1) can promote to Owner")
		return
	}

	_, err = h.DB.Exec(r.Context(),
		`UPDATE users SET power_level = $2 WHERE id = $1`, id, req.PowerLevel)
	if err != nil {
		slog.Error("set power", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
