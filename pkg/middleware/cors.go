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
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowedOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", RequestIDHeader},
		ExposedHeaders:   []string{RequestIDHeader},
		AllowCredentials: len(allowedOrigins) > 0,
		MaxAge:           300,
	})
}
