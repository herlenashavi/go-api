// Package middleware provides the chi-compatible HTTP middleware go-api
// mounts by default: request ID propagation, panic recovery, access
// logging, RED metrics, tracing, timeouts, body-size limits, CORS, and
// security headers. Each is independently usable — app.New wires a sensible
// default chain, but nothing here requires app.New.
package middleware

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// RequestIDHeader is the header used to read/propagate the request ID. It
// matches the de facto standard used by most load balancers and API
// gateways, so an ID assigned upstream survives end-to-end.
const RequestIDHeader = "X-Request-ID"

// RequestID assigns a request ID (reusing an inbound X-Request-ID if the
// caller/gateway already set one) and both stores it in context (retrieve
// with reqctx.RequestID) and echoes it on the response.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if id == "" {
			id = uuid.NewString()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := reqctx.WithRequestID(r.Context(), id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
