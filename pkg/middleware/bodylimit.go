package middleware

import "net/http"

// BodyLimit caps request body size at maxBytes using http.MaxBytesReader. A
// handler that then tries to read past the limit gets an *http.MaxBytesError
// from its Body.Read call; httpx's Decode maps that to a 413 problem — see
// pkg/httpx. maxBytes <= 0 disables the limit (not recommended for any
// internet-facing route).
func BodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if maxBytes <= 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}
