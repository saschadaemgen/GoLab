package handler

import (
	"encoding/json"
	"net/http"
)

// SecurityHeaders applies conservative defaults that prevent common
// browser-side attacks.
//
// - X-Content-Type-Options: stops MIME-sniffing so a text response can't
//   be reinterpreted as a script.
// - X-Frame-Options: DENY prevents clickjacking via iframe embedding.
// - Referrer-Policy: strict-origin-when-cross-origin keeps the path out of
//   referers sent to other origins.
// - Permissions-Policy: disables camera/mic/geolocation, features we don't
//   use and don't want a compromised script to claim.
// - X-XSS-Protection: 0 per modern OWASP guidance (the legacy filter
//   caused more problems than it solved).
//
// Content-Security-Policy is intentionally NOT set here - we still use
// inline styles and inline scripts in the flash-prevention shim. A tight
// CSP is a separate task.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-XSS-Protection", "0")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

// RateLimited returns a 429 with a readable JSON body. Attach to an
// httprate limiter with WithLimitHandler so the response shape matches
// every other API error the frontend handles.
func RateLimited(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusTooManyRequests)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "too many requests, try again later",
	})
}
