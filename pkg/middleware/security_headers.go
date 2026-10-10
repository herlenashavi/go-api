package middleware

import "net/http"

// SecurityHeaders adds a conservative set of standard security headers to
// every response. It deliberately does not attempt to hardcode a
// Content-Security-Policy for a bundled docs UI — that CSP is specific to
// whatever docs UI (if any) a service serves, is brittle to pin by script
// hash, and belongs in that service's own middleware if it ships
// interactive docs. Pass csp to set one; "" omits the header.
func SecurityHeaders(csp string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			if csp != "" {
				h.Set("Content-Security-Policy", csp)
			}
			next.ServeHTTP(w, r)
		})
	}
}
