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

// RequireAdmin blocks users whose power_level is below 100. Place it
// after RequireAuth (or RequireAuthRedirect for HTML pages).
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := auth.UserFromContext(r.Context())
		if u == nil || u.PowerLevel < 100 {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminHandler serves the /admin dashboard and its API companions.
type AdminHandler struct {
	DB     *pgxpool.Pool
	Render *render.Engine
	Users  *model.UserStore
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
	Stats    adminStats
	Users    []adminUser
	Channels []adminChannel
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
	_, err = h.DB.Exec(r.Context(),
		`UPDATE users SET power_level = $2 WHERE id = $1`, id, req.PowerLevel)
	if err != nil {
		slog.Error("set power", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}
