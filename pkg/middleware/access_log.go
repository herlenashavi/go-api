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
	// IgnorePaths skips logging entirely for any request path containing
	// one of these substrings (e.g. "/health/liveness"). Applied
	// consistently regardless of log level — canvass-fastapi's equivalent
	// bypassed this list whenever the logger was in debug mode, which
	// defeated the point of having an ignore list in the first place.
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
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request completed",
				slog.String("method", r.Method),
				slog.String("route", route),
				slog.Int("status", sw.status),
				slog.Int64("duration_ms", duration.Milliseconds()),
				slog.Int("bytes", sw.bytes),
			)
		})
	}
}

func shouldIgnore(path string, ignorePaths []string) bool {
	for _, p := range ignorePaths {
		if strings.Contains(path, p) {
			return true
		}
	}
	return false
}

// routePattern returns the matched chi route pattern, falling back to the
// raw path if routing metadata isn't available (e.g. in unit tests that
// call a handler directly without going through a chi.Mux).
func routePattern(r *http.Request) string {
	if rctx := chi.RouteContext(r.Context()); rctx != nil {
		if p := rctx.RoutePattern(); p != "" {
			return p
		}
	}
	return r.URL.Path
}

// maxLoggedBodyBytes bounds how much of a buffered body is actually
// unmarshalled and logged. It does NOT bound how much is read off the wire
// for restoration below — truncating that would corrupt the body the real
// handler receives. Pair with BodyLimit so request bodies are bounded
// before they ever reach here.
const maxLoggedBodyBytes = 1 << 20 // 1 MiB

// logRequestBody reads and logs the body, then replaces r.Body with a fresh
// reader over the same bytes so the handler further down the chain can
// still read the complete, untruncated body from the start. Reading r.Body
// here without restoring it would silently hand the handler an empty body
// on every logged request — easy to miss since it only breaks under the
// exact log level this middleware is gated on.
func logRequestBody(r *http.Request, logger *slog.Logger) {
	if r.Body == nil || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return
	}

	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(data))
	if err != nil || len(data) > maxLoggedBodyBytes {
		return // restored above either way; just don't log it
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return // not valid JSON — nothing to log
	}
	logger.DebugContext(r.Context(), "request body", slog.Any("body", logging.Redact(v)))
}
