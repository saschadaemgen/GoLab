package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

// ReadingHandler serves the per-client heartbeat endpoint that
// drives the reading tracker. Authenticated only; the rate limit
// is wired in main.go (240 / hour / user matches the briefing).
type ReadingHandler struct {
	Reading *model.ReadingStore
}

// Heartbeat accepts one HeartbeatRequest, validates and caps the
// payload, and forwards to ReadingStore.RecordHeartbeat. Responds
// 204 on success, 401 for anonymous callers, 400 for unparseable
// JSON, 500 on persistence failure.
//
// Caps applied here (mirrored on the client but enforced server-
// side because the client cannot be trusted):
//   - active_seconds clamped to [0, MaxActiveSecondsPerMinute]
//   - visible_posts truncated to MaxPostsPerHeartbeat entries
//   - each visible_posts.seconds_visible clamped to
//     [0, HeartbeatIntervalSeconds]
func (h *ReadingHandler) Heartbeat(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var req model.HeartbeatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	cfg, err := h.Reading.LoadConfig(r.Context())
	if err != nil {
		// LoadConfig already falls back to baked-in defaults on
		// query failure; logging is enough.
		slog.Warn("heartbeat: load config", "err", err)
	}

	if req.ActiveSeconds < 0 {
		req.ActiveSeconds = 0
	}
	if req.ActiveSeconds > cfg.MaxActiveSecondsPerMinute {
		req.ActiveSeconds = cfg.MaxActiveSecondsPerMinute
	}

	if len(req.VisiblePosts) > cfg.MaxPostsPerHeartbeat {
		req.VisiblePosts = req.VisiblePosts[:cfg.MaxPostsPerHeartbeat]
	}

	for i := range req.VisiblePosts {
		if req.VisiblePosts[i].SecondsVisible < 0 {
			req.VisiblePosts[i].SecondsVisible = 0
		}
		if req.VisiblePosts[i].SecondsVisible > cfg.HeartbeatIntervalSeconds {
			req.VisiblePosts[i].SecondsVisible = cfg.HeartbeatIntervalSeconds
		}
	}

	if err := h.Reading.RecordHeartbeat(r.Context(), user.ID, req); err != nil {
		slog.Error("heartbeat persist",
			"user_id", user.ID,
			"posts_count", len(req.VisiblePosts),
			"topics_count", len(req.TopicsEntered),
			"err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
