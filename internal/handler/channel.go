package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

var slugRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

type ChannelHandler struct {
	Channels *model.ChannelStore
	Users    *model.UserStore
}

type createChannelRequest struct {
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ChannelType string `json:"channel_type"`
}

func (h *ChannelHandler) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	var req createChannelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.Slug = strings.TrimSpace(strings.ToLower(req.Slug))
	req.Name = strings.TrimSpace(req.Name)

	if !slugRegex.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug must be 3-64 lowercase alphanumeric characters or hyphens")
		return
	}

	if len(req.Name) < 1 || len(req.Name) > 128 {
		writeError(w, http.StatusBadRequest, "name must be 1-128 characters")
		return
	}

	// Default to public
	if req.ChannelType == "" {
		req.ChannelType = "public"
	}
	validTypes := map[string]bool{"public": true, "private": true, "project": true, "announce": true}
	if !validTypes[req.ChannelType] {
		writeError(w, http.StatusBadRequest, "invalid channel type")
		return
	}

	// Check slug uniqueness
	existing, err := h.Channels.FindBySlug(r.Context(), req.Slug)
	if err != nil {
		slog.Error("create channel: find by slug", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if existing != nil {
		writeError(w, http.StatusConflict, "channel slug already taken")
		return
	}

	ch, err := h.Channels.Create(r.Context(), req.Slug, req.Name, req.Description, req.ChannelType, user.ID)
	if err != nil {
		slog.Error("create channel", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	slog.Info("channel created", "slug", ch.Slug, "creator", user.Username)
	writeJSON(w, http.StatusCreated, map[string]any{"channel": ch})
}

func (h *ChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 20
	offset := 0

	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	channels, err := h.Channels.ListPublic(r.Context(), limit, offset)
	if err != nil {
		slog.Error("list channels", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if channels == nil {
		channels = []model.Channel{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"channels": channels})
}

func (h *ChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	ch, err := h.Channels.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("get channel", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	// Check membership for the current user
	user := auth.UserFromContext(r.Context())
	isMember := false
	if user != nil {
		isMember, _ = h.Channels.IsMember(r.Context(), ch.ID, user.ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"channel":   ch,
		"is_member": isMember,
	})
}

func (h *ChannelHandler) Join(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	slug := chi.URLParam(r, "slug")

	ch, err := h.Channels.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("join channel: find", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	if user.PowerLevel < ch.PowerRequired {
		writeError(w, http.StatusForbidden, "insufficient power level")
		return
	}

	if err := h.Channels.Join(r.Context(), ch.ID, user.ID); err != nil {
		slog.Error("join channel", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "joined"})
}

func (h *ChannelHandler) Leave(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	slug := chi.URLParam(r, "slug")

	ch, err := h.Channels.FindBySlug(r.Context(), slug)
	if err != nil {
		slog.Error("leave channel: find", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if ch == nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	if err := h.Channels.Leave(r.Context(), ch.ID, user.ID); err != nil {
		slog.Error("leave channel", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "left"})
}
