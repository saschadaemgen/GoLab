package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
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
	Ratings       *model.ApplicationRatingStore // Sprint Y
	Hub           *Hub
}

type adminStats struct {
	Users  int `json:"users"`
	Posts  int `json:"posts"`
	// Sprint 13 UI cleanup: the dashboard now counts Spaces instead
	// of Channels because Channels are no longer surfaced in the UI.
	// The DB column is called "spaces" since migration 016.
	Spaces int `json:"spaces"`
	Banned int `json:"banned"`
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

// pendingUserView pairs a pending applicant with their existing
// rating row (if any). Sprint Y: the admin /admin page renders one
// of these per pending user so each row gets its five star widgets
// pre-populated with whatever the admin scored on a previous visit.
// A user without any rating row yet shows up with a zero-value
// ApplicationRating (every dimension nil).
type pendingUserView struct {
	User   model.User
	Rating *model.ApplicationRating
}

type adminDashboard struct {
	Stats               adminStats
	Users               []adminUser
	PendingUsers        []pendingUserView // Sprint 12 moderation queue + Sprint Y ratings
	RequireApproval     bool              // current state of the toggle
	AllowUsernameChange bool              // Sprint 13: user-facing username editor
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
		Users: []adminUser{},
	}
	ctx := r.Context()

	// Stats. "Spaces" replaced the old "Channels" counter in Sprint 13;
	// Channels stay in the DB but are no longer surfaced in the UI.
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&out.Stats.Users)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM posts`).Scan(&out.Stats.Posts)
	_ = h.DB.QueryRow(ctx, `SELECT COUNT(*) FROM spaces`).Scan(&out.Stats.Spaces)
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

	// Channels list was removed from the admin UI in Sprint 13 (the
	// `channels` table still exists for back-compat but users navigate
	// by Space now). If this ever needs to come back, query via
	// channel_members + posts.

	// Sprint 12 moderation data: pending users queue + require_approval
	// setting current state for the toggle.
	//
	// Sprint Y: also bulk-load the per-user rating rows so each star
	// widget renders with the previously-saved value. AttachTo
	// returns a zero-value rating for users with no row yet so the
	// template can iterate uniformly.
	if h.Users != nil {
		if pending, err := h.Users.ListPending(ctx, 50); err == nil {
			ids := make([]int64, len(pending))
			for i := range pending {
				ids[i] = pending[i].ID
			}
			var ratings map[int64]*model.ApplicationRating
			if h.Ratings != nil {
				ratings, _ = h.Ratings.AttachTo(ctx, ids)
			}
			views := make([]pendingUserView, 0, len(pending))
			for i := range pending {
				v := pendingUserView{User: pending[i]}
				if r, ok := ratings[pending[i].ID]; ok && r != nil {
					v.Rating = r
				} else {
					v.Rating = &model.ApplicationRating{UserID: pending[i].ID}
				}
				views = append(views, v)
			}
			out.PendingUsers = views
		}
	}
	if h.Settings != nil {
		out.RequireApproval = h.Settings.GetBool(ctx, "require_approval")
		out.AllowUsernameChange = h.Settings.GetBool(ctx, "allow_username_change")
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

// rateRequest is the body shape for PUT /api/admin/users/{id}/rating.
// `Value` is *int so a JSON null clears the dimension; an explicit 0
// is rejected by the model layer (CHECK constraint enforces 1-10).
type rateRequest struct {
	Dimension string `json:"dimension"`
	Value     *int   `json:"value"`
}

// notesRequest is the body shape for PUT /api/admin/users/{id}/rating/notes.
type notesRequest struct {
	Notes string `json:"notes"`
}

// ratingNotesMaxLen caps the admin notes blob. Long enough for a
// paragraph or two of moderation context, short enough to keep the
// admin panel responsive when the page renders 50 pending users at
// once. Sprint Y.
const ratingNotesMaxLen = 2000

// ratingResponse is what every rating mutation endpoint returns:
// the live average + the count of rated dimensions. The admin UI
// uses both to update the summary row without a second roundtrip.
type ratingResponse struct {
	Average    float64 `json:"average"`
	RatedCount int     `json:"rated_count"`
}

// SetRating updates one dimension of an applicant's rating row.
// Sprint Y per-click auto-save: clicking a star fires this directly
// instead of waiting on a parent Save button. The dimension allow-
// list lives in model.AllowedRatingDimensions; the value range is
// enforced by both the model and the DB CHECK constraint.
func (h *AdminHandler) SetRating(w http.ResponseWriter, r *http.Request) {
	if h.Ratings == nil {
		writeError(w, http.StatusServiceUnavailable, "ratings disabled")
		return
	}
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
	var req rateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !model.AllowedRatingDimensions[req.Dimension] {
		writeError(w, http.StatusBadRequest, "invalid dimension")
		return
	}
	if req.Value != nil && (*req.Value < 1 || *req.Value > 10) {
		writeError(w, http.StatusBadRequest, "value must be 1-10 or null")
		return
	}
	if err := h.Ratings.SetDimension(r.Context(), id, req.Dimension, req.Value, actor.ID); err != nil {
		slog.Error("set rating dimension", "error", err, "user", id, "dim", req.Dimension)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	rating, err := h.Ratings.Get(r.Context(), id)
	if err != nil {
		// The write succeeded; if the read-back fails we still want to
		// tell the client the click landed. Return zero average rather
		// than 500.
		slog.Warn("re-read rating after set", "error", err, "user", id)
		writeJSON(w, http.StatusOK, ratingResponse{})
		return
	}
	writeJSON(w, http.StatusOK, ratingResponse{
		Average:    rating.Average(),
		RatedCount: rating.RatedCount(),
	})
}

// GetRating returns the full rating row for a user. Used when the
// admin re-opens an already-approved user to adjust their score.
func (h *AdminHandler) GetRating(w http.ResponseWriter, r *http.Request) {
	if h.Ratings == nil {
		writeError(w, http.StatusServiceUnavailable, "ratings disabled")
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	rating, err := h.Ratings.Get(r.Context(), id)
	if err != nil {
		slog.Error("get rating", "error", err, "user", id)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rating":      rating,
		"average":     rating.Average(),
		"rated_count": rating.RatedCount(),
	})
}

// SetRatingNotes stores the admin's free-form moderation note.
// Debounced on the client so the user doesn't hit this on every
// keystroke; the handler still gets called once typing settles.
func (h *AdminHandler) SetRatingNotes(w http.ResponseWriter, r *http.Request) {
	if h.Ratings == nil {
		writeError(w, http.StatusServiceUnavailable, "ratings disabled")
		return
	}
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
	var req notesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Notes) > ratingNotesMaxLen {
		writeError(w, http.StatusBadRequest, "notes too long")
		return
	}
	if err := h.Ratings.SetNotes(r.Context(), id, strings.TrimSpace(req.Notes), actor.ID); err != nil {
		slog.Error("set rating notes", "error", err, "user", id)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
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
	"require_approval":      true,
	"allow_username_change": true,
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

// SetUsername lets an admin rename any user. Bypasses the
// allow_username_change platform toggle (that's a user-facing gate
// only). Guarded by the usual power-level rules: admins cannot
// rename users at or above their own level, and nobody can rename
// themselves via this endpoint (they use /settings for that).
type setUsernameReq struct {
	Username string `json:"username"`
}

func (h *AdminHandler) SetUsername(w http.ResponseWriter, r *http.Request) {
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
	if actor.ID == id {
		writeError(w, http.StatusBadRequest, "use /settings to change your own username")
		return
	}

	var req setUsernameReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if !isValidUsername(req.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3-32 alphanumeric characters or underscores")
		return
	}

	// Power-level guard: admins cannot rename users at or above their
	// level. Owners (100) can rename anyone but themselves.
	target, err := h.Users.FindByID(r.Context(), id)
	if err != nil {
		slog.Error("admin set username: find user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "user not found")
		return
	}
	if target.PowerLevel >= actor.PowerLevel {
		writeError(w, http.StatusForbidden, "cannot rename a user at or above your power level")
		return
	}

	// Case-insensitive uniqueness check. If the only collision is the
	// target themselves (case change of own handle) we allow it.
	taken, err := h.Users.UsernameExists(r.Context(), req.Username)
	if err != nil {
		slog.Error("admin set username: check", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if taken && !strings.EqualFold(req.Username, target.Username) {
		writeError(w, http.StatusConflict, "username already taken")
		return
	}

	if err := h.Users.UpdateUsername(r.Context(), id, req.Username); err != nil {
		slog.Error("admin set username: update", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	slog.Info("admin renamed user",
		"actor_id", actor.ID, "target_id", id,
		"old", target.Username, "new", req.Username)
	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "username": req.Username})
}
