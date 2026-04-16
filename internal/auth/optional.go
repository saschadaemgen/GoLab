package auth

import (
	"context"
	"net/http"

	"github.com/saschadaemgen/GoLab/internal/model"
)

// CurrentUser reads the session cookie (either __Host-golab_session or the
// legacy session_id) and returns the user if present, or nil if the visitor
// is not logged in. It never errors: missing or invalid sessions simply
// return nil.
func CurrentUser(r *http.Request, sessions *SessionStore, users *model.UserStore) *model.User {
	value := readSessionCookie(r)
	if value == "" {
		return nil
	}
	userID, err := sessions.Find(r.Context(), value)
	if err != nil {
		return nil
	}
	user, err := users.FindByID(r.Context(), userID)
	if err != nil {
		return nil
	}
	return user
}

// OptionalAuth attaches the current user to the request context if logged in,
// but does not reject unauthenticated requests. Use on pages that render
// differently based on login state (home, explore, profile).
func OptionalAuth(sessions *SessionStore, users *model.UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := CurrentUser(r, sessions, users)
			ctx := r.Context()
			if user != nil {
				ctx = context.WithValue(ctx, userContextKey, user)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAuthRedirect rejects unauthenticated browser requests by
// redirecting to /login instead of returning 401 JSON. Use on pages
// that require authentication (feed, compose, etc.).
func RequireAuthRedirect(sessions *SessionStore, users *model.UserStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := CurrentUser(r, sessions, users)
			if user == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
