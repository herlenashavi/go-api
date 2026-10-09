package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// Recover catches panics anywhere further down the chain, logs them with a
// stack trace, and responds with a 500 problem+json instead of letting
// net/http close the connection with a bare "500 Internal Server Error" and
// no body. debug controls whether the panic value is echoed to the client
// (see problem.Internal) — leave false in production.
func Recover(logger *slog.Logger, debugMode bool) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					err := fmt.Errorf("panic: %v", rec)
					logger.ErrorContext(r.Context(), "unhandled panic",
						"error", err, "stack", string(debug.Stack()))
					problem.Write(w, r, problem.Internal(err, debugMode))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
