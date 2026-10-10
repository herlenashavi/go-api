// Package health provides liveness/readiness endpoints and a Prometheus
// scrape endpoint, mounted outside any auth/authz middleware — a load
// balancer or orchestrator hitting these should never need credentials.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

// Checker reports whether a dependency (database, cache, upstream API) is
// currently reachable. It is called with a context bounded by Router's
// PerCheckTimeout. A well-behaved Checker returns promptly when ctx is
// canceled; one that doesn't (a blocking driver call with no context
// support, like canvass-fastapi's smtplib/psycopg2 health checks) still
// can't hang the readiness endpoint itself — Router stops waiting at the
// timeout and reports that check as unhealthy regardless, leaving the
// still-running goroutine to finish on its own.
type Checker func(ctx context.Context) error

// Check names a single readiness dependency.
type Check struct {
	Name    string
	Checker Checker
}

// Config controls Router.
type Config struct {
	Checks []Check
	// PerCheckTimeout bounds how long any one Checker is waited on.
	// Defaults to 2s.
	PerCheckTimeout time.Duration
	// MetricsHandler, if set, is mounted at /health/metrics (Prometheus
	// text exposition format) — pass telemetry.Provider.MetricsHandler.
	MetricsHandler http.Handler
}

// CheckResult is one Check's outcome, as reported by both Router's
// /readiness endpoint and grpcx's gRPC health service — both call Run, so
// a dependency failure looks identical over HTTP and gRPC.
type CheckResult struct {
	Name    string `json:"name"`
	Healthy bool   `json:"healthy"`
	Error   string `json:"error,omitempty"`
}

type readinessResponse struct {
	Status string        `json:"status"`
	Checks []CheckResult `json:"checks,omitempty"`
}

// Router returns a chi.Router exposing:
//
//	GET /health/liveness  — always 200 once the process is serving traffic.
//	GET /health/readiness — 200 iff every Check passes, 503 otherwise, with
//	                         a body detailing which check(s) failed.
//	GET /health/metrics   — Prometheus scrape endpoint, if MetricsHandler
//	                         is configured.
func Router(cfg Config) chi.Router {
	timeout := cfg.PerCheckTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}

	r := chi.NewRouter()

	r.Get("/liveness", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "live"})
	})

	r.Get("/readiness", func(w http.ResponseWriter, r *http.Request) {
		healthy, results := Run(r.Context(), cfg.Checks, timeout)

		status, overall := http.StatusOK, "healthy"
		if !healthy {
			status, overall = http.StatusServiceUnavailable, "unhealthy"
		}
		writeJSON(w, status, readinessResponse{Status: overall, Checks: results})
	})

	if cfg.MetricsHandler != nil {
		r.Get("/metrics", cfg.MetricsHandler.ServeHTTP)
	}

	return r
}

// Run runs every Check concurrently and returns once all of them have
// either reported a result or hit timeout — whichever is sooner, per check
// — plus whether every one was healthy. It deliberately does not
// sync.WaitGroup.Wait() on the checker goroutines themselves: if a Checker
// ignores context cancellation and blocks forever, waiting for it to
// actually return would defeat the timeout entirely. Each goroutine
// instead reports to a buffered channel it owns, so a late (or never)
// result has somewhere to land without blocking anything — the goroutine
// may leak until its underlying call unblocks, but the caller (Router's
// /readiness, or grpcx's gRPC health service) is never held hostage by it.
func Run(ctx context.Context, checks []Check, timeout time.Duration) (healthy bool, results []CheckResult) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	results = make([]CheckResult, len(checks))
	// wg tracks only the *watcher* goroutines below, each of which is
	// bounded by timeout by construction — never the Checker goroutines
	// they watch, which are not waited on at all (see doc comment).
	var wg sync.WaitGroup
	wg.Add(len(checks))

	for i, c := range checks {
		i, c := i, c
		resultCh := make(chan CheckResult, 1)

		go func() {
			checkCtx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			err := c.Checker(checkCtx)
			res := CheckResult{Name: c.Name, Healthy: err == nil}
			if err != nil {
				res.Error = err.Error()
			}
			resultCh <- res // buffered: never blocks, even if the watcher already gave up
		}()

		go func() {
			defer wg.Done()
			select {
			case res := <-resultCh:
				results[i] = res
			case <-time.After(timeout):
				results[i] = CheckResult{Name: c.Name, Healthy: false, Error: "check timed out"}
			}
		}()
	}

	wg.Wait() // bounded: every watcher above returns within timeout

	healthy = true
	for _, res := range results {
		if !res.Healthy {
			healthy = false
			break
		}
	}
	return healthy, results
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
