package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

var (
	usernameRegex = regexp.MustCompile(`^[a-zA-Z0-9_]{3,32}$`)
	emailRegex    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
)

type AuthHandler struct {
	Users         *model.UserStore
	Sessions      *auth.SessionStore
	Settings      *model.SettingsStore
	Notifications *model.NotificationStore
	Hub           *Hub // optional, used to push notification badges
	Secure        bool // true in production (HTTPS)
}

type registerRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// wantsFormResponse reports whether the caller submitted an HTML form
// (application/x-www-form-urlencoded or multipart/form-data). Those
// callers get an HTTP 303 redirect on success so the URL bar ends up
// clean. AJAX/JSON callers get JSON.
func wantsFormResponse(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	return strings.HasPrefix(ct, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(ct, "multipart/form-data")
}

// decodeRegister reads a registration request from either JSON or
// form-encoded body. Returns the parsed request or an error.
func decodeRegister(r *http.Request) (registerRequest, error) {
	var req registerRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.Username = r.Form.Get("username")
		req.Email = r.Form.Get("email")
		req.Password = r.Form.Get("password")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// decodeLogin reads a login request from either JSON or form-encoded body.
func decodeLogin(r *http.Request) (loginRequest, error) {
	var req loginRequest
	if wantsFormResponse(r) {
		if err := r.ParseForm(); err != nil {
			return req, err
		}
		req.Email = r.Form.Get("email")
		req.Password = r.Form.Get("password")
		return req, nil
	}
	err := json.NewDecoder(r.Body).Decode(&req)
	return req, err
}

// redirectOrJSON handles the success path based on how the client submitted.
// Form submissions get a 303 See Other to `path` so the browser does a fresh
// GET and the URL bar loses the POST context. AJAX callers get `jsonBody`.
func redirectOrJSON(w http.ResponseWriter, r *http.Request, path string, jsonBody any) {
	if wantsFormResponse(r) {
		http.Redirect(w, r, path, http.StatusSeeOther)
		return
	}
	writeJSON(w, http.StatusOK, jsonBody)
}

// errorRedirectOrJSON reports an error either as JSON (AJAX) or as a
// redirect back to errorPath with ?error=... (form fallback). Keeps the
// password out of the URL even in the error path.
func errorRedirectOrJSON(w http.ResponseWriter, r *http.Request, errorPath string, status int, message string) {
	if wantsFormResponse(r) {
		http.Redirect(w, r, errorPath+"?error="+urlEncode(message), http.StatusSeeOther)
		return
	}
	writeError(w, status, message)
}

func urlEncode(s string) string {
	// Minimal URL-encode: only encode the characters that break a query string.
	// net/url would be fuller but we want to avoid adding an import path here
	// just for this helper.
	r := strings.NewReplacer(
		"%", "%25",
		"&", "%26",
		"?", "%3F",
		" ", "+",
		"\n", "%0A",
	)
	return r.Replace(s)
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRegister(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	// Validate username
	if !usernameRegex.MatchString(req.Username) {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, "username must be 3-32 alphanumeric characters or underscores")
		return
	}

	// Validate email
	if !emailRegex.MatchString(req.Email) {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, "invalid email address")
		return
	}

	// Validate password per NIST SP 800-63B: length only, 8-128 chars.
	if err := validatePassword(req.Password); err != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusBadRequest, err.Error())
		return
	}

	// Check username uniqueness
	existing, err := h.Users.FindByUsername(r.Context(), req.Username)
	if err != nil {
		slog.Error("register: find by username", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusConflict, "username already taken")
		return
	}

	// Check email uniqueness
	existing, err = h.Users.FindByEmail(r.Context(), req.Email)
	if err != nil {
		slog.Error("register: find by email", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		errorRedirectOrJSON(w, r, "/register", http.StatusConflict, "email already registered")
		return
	}

	// Hash password
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("register: hash password", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}

	// Determine initial status from the platform setting. First user
	// (id will be 1) always ends up active regardless - Store.Create
	// enforces that invariant so the platform isn't DOA behind its
	// own approval gate.
	status := model.UserStatusActive
	if h.Settings != nil && h.Settings.GetBool(r.Context(), "require_approval") {
		status = model.UserStatusPending
	}

	user, err := h.Users.Create(r.Context(), req.Username, req.Email, hash, status)
	if err != nil {
		slog.Error("register: create user", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}

	// Session is created either way - pending users can log in and see
	// a read-only view of the platform until admins approve them.
	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("register: create session", "error", err)
		errorRedirectOrJSON(w, r, "/register", http.StatusInternalServerError, "internal error")
		return
	}
	h.setSessionCookie(w, sessionID)

	slog.Info("user registered", "username", user.Username, "id", user.ID, "status", user.Status)

	// If pending, notify every admin so they can review the queue.
	if user.Status == model.UserStatusPending {
		h.notifyAdminsOfNewUser(r.Context(), user)
		redirectOrJSON(w, r, "/pending", map[string]any{"user": user, "status": "pending"})
		return
	}

	redirectOrJSON(w, r, "/feed", map[string]any{"user": user})
}

// notifyAdminsOfNewUser fans out a "new_user" notification (plus a
// WebSocket count-push when a hub is available) so admins see the
// pending badge grow in real time.
func (h *AuthHandler) notifyAdminsOfNewUser(ctx context.Context, newUser *model.User) {
	if h.Users == nil || h.Notifications == nil {
		return
	}
	admins, err := h.Users.ListAdmins(ctx)
	if err != nil {
		slog.Error("register: list admins", "error", err)
		return
	}
	for _, admin := range admins {
		if admin.ID == newUser.ID {
			continue // skip self (first user case, though they're active anyway)
		}
		if _, err := h.Notifications.Create(ctx, admin.ID, newUser.ID, model.NotifNewUser, nil); err != nil {
			slog.Warn("register: create admin notif", "admin", admin.ID, "error", err)
			continue
		}
		if h.Hub != nil {
			count, _ := h.Notifications.UnreadCount(ctx, admin.ID)
			h.Hub.PublishToUser(admin.ID, Message{
				Type: "notification_count",
				Data: map[string]int{"count": count},
			})
		}
	}
}

// validatePassword enforces the NIST SP 800-63B recommendation of length
// only, no composition rules. Minimum 8, maximum 128 characters.
func validatePassword(pw string) error {
	if len(pw) < 8 {
		return errPasswordTooShort
	}
	if len(pw) > 128 {
		return errPasswordTooLong
	}
	return nil
}

var (
	errPasswordTooShort = &passwordError{"password must be at least 8 characters"}
	errPasswordTooLong  = &passwordError{"password must not exceed 128 characters"}
)

type passwordError struct{ msg string }

func (e *passwordError) Error() string { return e.msg }

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	req, err := decodeLogin(r)
	if err != nil {
		errorRedirectOrJSON(w, r, "/login", http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	user, err := h.Users.FindByEmail(r.Context(), req.Email)
	if err != nil {
		slog.Error("login: find by email", "error", err)
		errorRedirectOrJSON(w, r, "/login", http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		errorRedirectOrJSON(w, r, "/login", http.StatusUnauthorized, "invalid email or password")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		errorRedirectOrJSON(w, r, "/login", http.StatusUnauthorized, "invalid email or password")
		return
	}

	if user.Banned {
		errorRedirectOrJSON(w, r, "/login", http.StatusForbidden, "this account has been banned")
		return
	}

	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("login: create session", "error", err)
		errorRedirectOrJSON(w, r, "/login", http.StatusInternalServerError, "internal error")
		return
	}

	h.setSessionCookie(w, sessionID)

	slog.Info("user logged in", "username", user.Username, "id", user.ID)
	redirectOrJSON(w, r, "/feed", map[string]any{
		"user": user,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	// Look up under either cookie name so logging out works right after a
	// deploy switches the name (e.g. from session_id to __Host-golab_session).
	var value string
	for _, name := range []string{"__Host-golab_session", "session_id"} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			value = c.Value
			break
		}
	}
	if value != "" {
		if err := h.Sessions.Delete(r.Context(), value); err != nil {
			slog.Error("logout: delete session", "error", err)
		}
	}

	// Clear both possible cookie names defensively.
	for _, name := range []string{"__Host-golab_session", "session_id"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			Secure:   h.Secure,
			SameSite: http.SameSiteLaxMode,
		})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

// sessionCookieName returns the appropriate cookie name for the current
// deployment. In production (HTTPS) we use the "__Host-" prefix which the
// browser enforces: only accepted from HTTPS, must have Path=/, must not
// have a Domain attribute. This prevents subdomain cookie-injection
// attacks. In local dev we can't use it because we run plain HTTP.
func (h *AuthHandler) sessionCookieName() string {
	if h.Secure {
		return "__Host-golab_session"
	}
	return "session_id"
}

func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.sessionCookieName(),
		Value:    sessionID,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		Secure:   h.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
}
