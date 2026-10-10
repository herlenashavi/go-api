package middleware

import (
	"net/http"

	"github.com/herlenashavi/go-api/pkg/telemetry"
)

// MetricsConfig controls Metrics.
type MetricsConfig struct {
	Metrics *telemetry.HTTPMetrics
	// IgnorePaths excludes matching paths (substring match) from RED
	// metrics entirely — typically /health/* and /metrics itself, so
	// scrapers don't generate metrics about scraping.
	IgnorePaths []string
}

// Metrics records RED (rate/errors/duration) metrics for every request,
// labeled by method, route pattern, and status code.
func Metrics(cfg MetricsConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if shouldIgnore(r.URL.Path, cfg.IgnorePaths) {
				next.ServeHTTP(w, r)
				return
			}

			finish := cfg.Metrics.Start(r.Context(), r.Method)
			sw := newStatusWriter(w)
			next.ServeHTTP(sw, r)
			finish(routePattern(r), sw.status)
		})
	}
}
