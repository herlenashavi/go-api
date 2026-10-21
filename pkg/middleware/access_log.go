package middleware

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/logging"
)

// AccessLogConfig controls AccessLog.
type AccessLogConfig struct {
	Logger *slog.Logger
	// IgnorePaths skips logging entirely for any request path equal to, or
	// nested under, one of these prefixes ("/health" matches
	// "/health/liveness" but not "/v1/patients/{id}/health-records"). Applied
	// consistently regardless of log level — a debug-mode logger doesn't
	// bypass this list, which would defeat the point of having one.
	IgnorePaths []string
	// LogRequestBody logs the (redacted) JSON request body at debug level.
	// Only takes effect for Content-Type: application/json requests whose
	// path isn't in IgnorePaths.
	LogRequestBody bool
}

// AccessLog logs one line per completed request: method, route pattern,
// status, duration, and response size. Route is the chi route *pattern*
// (e.g. "/v1/heroes/{id}"), not the resolved path, keeping log volume and
// any log-based metrics low-cardinality.
func AccessLog(cfg AccessLogConfig) func(http.Handler) http.Handler {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldIgnore(r.URL.Path, cfg.IgnorePaths) {
				next.ServeHTTP(w, r)
				return
			}

			if cfg.LogRequestBody && logger.Enabled(r.Context(), slog.LevelDebug) {
				logRequestBody(r, logger)
			}

			sw := newStatusWriter(w)
			start := time.Now()
			next.ServeHTTP(sw, r)
			duration := time.Since(start)

			route := routePattern(r)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", sw.status),
				slog.Int64("duration_ms", duration.Milliseconds()),
				slog.Int("bytes", sw.bytes),
			}
			if route == unmatchedRoute {
				// No pattern to say what was requested, so log the path
				// itself — in its own field, never as the route label.
				attrs = append(attrs, slog.String("path", r.URL.Path))
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request completed", attrs...)
		})
	}
}

// shouldIgnore reports whether path is one of ignorePaths or sits beneath
// one of them as a whole path segment. A substring match would also hide
// unrelated routes that merely contain the word — "/health" swallowing
// "/v1/patients/{id}/health-records" — silently dropping them from logs and
// metrics.
func shouldIgnore(path string, ignorePaths []string) bool {
	for _, p := range ignorePaths {
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			continue
		}
		if path == p || strings.HasPrefix(path, p+"/") {
			return true
		}
	}
	return false
}

// unmatchedRoute labels a request no route pattern is known for.
const unmatchedRoute = "unmatched"

// routePattern returns the chi route pattern for r — the label AccessLog,
// Metrics, and Tracing all use — and never the raw path: a path carries
// IDs and whatever else a client puts in it, so labeling by it would give
// every 404 from a scanner its own metric series.
//
// The pattern normally comes from routing. A request that timed out never
// gets one (Timeout routes on a private copy of the route context that is
// discarded on timeout), so it's looked up from the router directly. A
// request that matches no route at all — a 404, a 405, or a handler
// called without a chi.Mux — is labeled unmatchedRoute.
func routePattern(r *http.Request) string {
	rctx := chi.RouteContext(r.Context())
	if rctx == nil {
		return unmatchedRoute
	}
	if p := rctx.RoutePattern(); p != "" {
		return p
	}
	if rctx.Routes != nil {
		path := r.URL.RawPath
		if path == "" {
			path = r.URL.Path
		}
		if p := rctx.Routes.Find(chi.NewRouteContext(), r.Method, path); p != "" {
			return p
		}
	}
	return unmatchedRoute
}

// maxLoggedBodyBytes bounds both how much of a body is logged and how much
// logRequestBody buffers to decide: it reads at most maxLoggedBodyBytes+1
// bytes, so a huge body costs this middleware a bounded amount of memory
// regardless of where it sits relative to BodyLimit in the chain.
const maxLoggedBodyBytes = 1 << 20 // 1 MiB

// logRequestBody logs the body, then replaces r.Body with a reader that
// yields the bytes already consumed followed by whatever is still unread,
// so the handler further down the chain receives the complete, untruncated
// body from the start. Reading r.Body here without restoring it would
// silently hand the handler an empty body on every logged request — easy
// to miss since it only breaks under the exact log level this middleware
// is gated on.
//
// Only a bounded prefix is ever buffered: a body longer than
// maxLoggedBodyBytes isn't logged, and its remainder is streamed straight
// through to the handler rather than read into memory here.
func logRequestBody(r *http.Request, logger *slog.Logger) {
	if r.Body == nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, maxLoggedBodyBytes+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(data), r.Body), r.Body}
	if err != nil || len(data) > maxLoggedBodyBytes {
		return // restored above either way; just don't log it
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return // not valid JSON — nothing to log
	}
	// Two independent redaction passes apply here, not one: logging.Redact
	// strips known-sensitive *keys* from the parsed body right now; every
	// logger built by logging.New also runs the resulting value through
	// its own, broader PII-redacting handler (pkg/logging's pii.go) before
	// it's ever encoded — catching, e.g., an email address embedded in a
	// free-text field this call's key-based pass has no way to know about.
	logger.DebugContext(r.Context(), "request body", slog.Any("body", logging.Redact(v)))
}
