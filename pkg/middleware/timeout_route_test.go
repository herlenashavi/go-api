package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

// timeoutRouter builds the shape app.New uses: AccessLog as router-level
// middleware outside Timeout, so chi's routing for the request happens
// inside Timeout's handler goroutine.
func timeoutRouter(buf *bytes.Buffer, d time.Duration, handler http.HandlerFunc) http.Handler {
	r := chi.NewRouter()
	r.Use(AccessLog(AccessLogConfig{Logger: jsonLogger(buf, slog.LevelInfo)}))
	r.Use(Timeout(d, nil, false))
	r.Get("/items/{id}", handler)
	return r
}

// TestTimeout_RoutePatternReachesOuterMiddleware: routing happens on
// Timeout's own copy of the route context, so the result has to be copied
// back for AccessLog/Metrics/Tracing outside it to label by route pattern
// rather than the raw, high-cardinality path.
func TestTimeout_RoutePatternReachesOuterMiddleware(t *testing.T) {
	var buf bytes.Buffer
	h := timeoutRouter(&buf, time.Second, func(w http.ResponseWriter, r *http.Request) {
		if got := chi.URLParam(r, "id"); got != "42" {
			t.Errorf("handler URLParam(id) = %q, want 42", got)
		}
		w.WriteHeader(http.StatusOK)
	})

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/items/42", nil))

	line := findLine(t, logLines(t, &buf), "request completed")
	if line["route"] != "/items/{id}" {
		t.Fatalf("route = %v, want the pattern /items/{id}", line["route"])
	}
}

// TestTimeout_TimedOutHandlerDoesNotRaceOuterMiddleware is a -race
// regression test: a handler still running after its timeout must not be
// writing the same route context that AccessLog reads (and that chi
// recycles for the next request) once Timeout has returned.
func TestTimeout_TimedOutHandlerDoesNotRaceOuterMiddleware(t *testing.T) {
	var buf bytes.Buffer
	release := make(chan struct{})
	finished := make(chan struct{})
	h := timeoutRouter(&buf, 10*time.Millisecond, func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		<-release
		_ = chi.URLParam(r, "id") // touch the route context after the timeout fired
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/items/7", nil))
	close(release)
	<-finished

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}
