package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type HomeHandler struct {
	DB *pgxpool.Pool
}

func (h *HomeHandler) Health(w http.ResponseWriter, r *http.Request) {
	if err := h.DB.Ping(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
