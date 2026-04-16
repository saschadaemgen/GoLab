package handler

import (
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
	Users    *model.UserStore
	Sessions *auth.SessionStore
	Secure   bool // true in production (HTTPS)
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

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	// Validate username
	if !usernameRegex.MatchString(req.Username) {
		writeError(w, http.StatusBadRequest, "username must be 3-32 alphanumeric characters or underscores")
		return
	}

	// Validate email
	if !emailRegex.MatchString(req.Email) {
		writeError(w, http.StatusBadRequest, "invalid email address")
		return
	}

	// Validate password
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	// Check username uniqueness
	existing, err := h.Users.FindByUsername(r.Context(), req.Username)
	if err != nil {
		slog.Error("register: find by username", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "username already taken")
		return
	}

	// Check email uniqueness
	existing, err = h.Users.FindByEmail(r.Context(), req.Email)
	if err != nil {
		slog.Error("register: find by email", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "email already registered")
		return
	}

	// Hash password
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		slog.Error("register: hash password", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Create user
	user, err := h.Users.Create(r.Context(), req.Username, req.Email, hash)
	if err != nil {
		slog.Error("register: create user", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	// Create session
	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("register: create session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.setSessionCookie(w, sessionID)

	slog.Info("user registered", "username", user.Username, "id", user.ID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"user": user,
	})
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))

	user, err := h.Users.FindByEmail(r.Context(), req.Email)
	if err != nil {
		slog.Error("login: find by email", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if user == nil {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if !auth.CheckPassword(user.PasswordHash, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}

	if user.Banned {
		writeError(w, http.StatusForbidden, "this account has been banned")
		return
	}

	sessionID, err := h.Sessions.Create(r.Context(), user.ID)
	if err != nil {
		slog.Error("login: create session", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	h.setSessionCookie(w, sessionID)

	slog.Info("user logged in", "username", user.Username, "id", user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user": user,
	})
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session_id")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	if err := h.Sessions.Delete(r.Context(), cookie.Value); err != nil {
		slog.Error("logout: delete session", "error", err)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.Secure,
		SameSite: http.SameSiteLaxMode,
	})

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

func (h *AuthHandler) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "session_id",
		Value:    sessionID,
		Path:     "/",
		MaxAge:   30 * 24 * 60 * 60, // 30 days
		HttpOnly: true,
		Secure:   h.Secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(30 * 24 * time.Hour),
	})
}
