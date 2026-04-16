package auth

import (
	"context"
	"net/http"

	"github.com/saschadaemgen/GoLab/internal/model"
)

type contextKey string

const userContextKey contextKey = "user"

// SessionCookieNames is the ordered list of cookie names we accept for
// session lookup. In production the primary name is __Host-golab_session
// (forced HTTPS, no Domain, Path=/). The legacy name is still read so a
// deploy that switches names doesn't log everybody out mid-session.
var SessionCookieNames = []string{"__Host-golab_session", "session_id"}

// readSessionCookie returns the first non-empty session cookie value, if any.
func readSessionCookie(r *http.Request) string {
	for _, name := range SessionCookieNames {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value
		}
	}
	return ""
}

func RequireAuth(sessions *SessionStore, users *model.UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := readSessionCookie(r)
			if value == "" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			userID, err := sessions.Find(r.Context(), value)
			if err != nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			user, err := users.FindByID(r.Context(), userID)
			if err != nil || user == nil {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserFromContext(ctx context.Context) *model.User {
	u, _ := ctx.Value(userContextKey).(*model.User)
	return u
}
