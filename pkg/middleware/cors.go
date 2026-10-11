package middleware

import (
	"net/http"

	"github.com/go-chi/cors"
)

// CORS builds a CORS middleware allowing only allowedOrigins (no origins ->
// no cross-origin requests permitted at all; go-api never defaults to "*",
// because "*" plus AllowCredentials is also an invalid combination
// browsers reject outright).
func CORS(allowedOrigins []string) func(http.Handler) http.Handler {
	opts := cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", RequestIDHeader},
		ExposedHeaders:   []string{RequestIDHeader},
		AllowCredentials: len(allowedOrigins) > 0,
		MaxAge:           300,
	}

	// go-chi/cors treats an empty AllowedOrigins as "allow every origin"
	// and answers with "Access-Control-Allow-Origin: *" — the opposite of
	// this function's and config.Base.CORSAllowedOrigins' documented
	// default, and it applies whenever CORS_ALLOWED_ORIGINS is simply
	// unset. An AllowOriginFunc suppresses that: it takes precedence over
	// the library's allow-all flag *and* stops the flag being set in the
	// first place, so no origin is permitted and no header is emitted.
	if len(allowedOrigins) == 0 {
		opts.AllowOriginFunc = denyAllOrigins
	}

	return cors.Handler(opts)
}

func denyAllOrigins(*http.Request, string) bool { return false }
