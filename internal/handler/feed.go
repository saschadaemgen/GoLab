package handler

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

type FeedHandler struct {
	Posts *model.PostStore
}

func (h *FeedHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	var before *time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err == nil {
			before = &t
		}
	}

	posts, err := h.Posts.Feed(r.Context(), user.ID, limit, before)
	if err != nil {
		slog.Error("get feed", "error", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if posts == nil {
		posts = []model.Post{}
	}

	writeJSON(w, http.StatusOK, map[string]any{"posts": posts})
}
