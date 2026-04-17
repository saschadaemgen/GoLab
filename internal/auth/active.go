package auth

import (
	"net/http"

	"github.com/saschadaemgen/GoLab/internal/model"
)

// RequireActiveUser blocks pending and rejected users from any
// mutation route. Applied to POST /api/posts, reactions, image
// uploads, channel actions, follows, etc. Read-only GET endpoints
// should NOT wrap with this - pending users are allowed to browse
// while waiting for approval.
//
// Must be chained AFTER RequireAuth so the user is already in the
// context. Returns 403 with a JSON error body so the client can
// surface a readable message.
func RequireActiveUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		switch u.Status {
		case model.UserStatusPending:
			http.Error(w, `{"error":"account pending approval"}`, http.StatusForbidden)
			return
		case model.UserStatusRejected:
			http.Error(w, `{"error":"account not approved"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
