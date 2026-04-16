package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/saschadaemgen/GoLab/internal/auth"
	"github.com/saschadaemgen/GoLab/internal/model"
)

// NotifDispatch is a thin wrapper that creates a notification in the DB
// and pushes a WebSocket event to the recipient when a hub is available.
// This is the type post.go / follow flows call into.
type NotifDispatch struct {
	Store *model.NotificationStore
	Hub   *Hub
}

func (d *NotifDispatch) Notify(ctx context.Context, userID, actorID int64, notifType string, postID *int64) {
	if d == nil || d.Store == nil {
		return
	}
	n, err := d.Store.Create(ctx, userID, actorID, notifType, postID)
	if err != nil {
		slog.Error("notify", "error", err)
		return
	}
	if n == nil || d.Hub == nil {
		return
	}

	// Update unread count for recipient; UI listens and animates the badge.
	count, _ := d.Store.UnreadCount(ctx, userID)
	d.Hub.PublishToUser(userID, Message{
		Type:    "notification_count",
		Data:    map[string]int{"count": count},
		Message: "",
	})
}

// NotifHandler serves REST endpoints for the notifications dropdown.
type NotifHandler struct {
	Store *model.NotificationStore
}

func (h *NotifHandler) List(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	items, err := h.Store.List(r.Context(), user.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if items == nil {
		items = []model.Notification{}
	}
	unread, _ := h.Store.UnreadCount(r.Context(), user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"notifications": items,
		"unread":        unread,
	})
}

func (h *NotifHandler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := h.Store.MarkAllRead(r.Context(), user.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *NotifHandler) MarkRead(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := h.Store.MarkRead(r.Context(), user.ID, id); err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Count returns just the unread count - used for badge refresh.
func (h *NotifHandler) Count(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	count, err := h.Store.UnreadCount(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": count})
}

// readBody is a small helper to silence json.Decoder's body-leak warning when
// a handler doesn't care about the request body (e.g. POST mark-read).
func readBody(r *http.Request) {
	_ = json.NewDecoder(r.Body).Decode(new(struct{}))
}
