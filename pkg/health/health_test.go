package health_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/herlenashavi/go-api/pkg/health"
)

func TestRouter_ReadinessAllHealthy(t *testing.T) {
	r := health.Router(health.Config{
		Checks: []health.Check{
			{Name: "db", Checker: func(ctx context.Context) error { return nil }},
		},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readiness", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
}

func TestRouter_ReadinessUnhealthyDependency(t *testing.T) {
	r := health.Router(health.Config{
		Checks: []health.Check{
			{Name: "cache", Checker: func(ctx context.Context) error { return context.DeadlineExceeded }},
		},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readiness", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
}

// TestRouter_ReadinessBoundsHangingChecker is the regression test for the
// exact failure mode canvass-fastapi's smtplib/psycopg2 health checks had:
// a Checker that ignores context cancellation and blocks forever must not
// be able to hang the readiness endpoint past PerCheckTimeout.
func TestRouter_ReadinessBoundsHangingChecker(t *testing.T) {
	r := health.Router(health.Config{
		PerCheckTimeout: 30 * time.Millisecond,
		Checks: []health.Check{
			{Name: "stuck", Checker: func(ctx context.Context) error {
				select {} // never returns, and ignores ctx entirely
			}},
		},
	})

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readiness", nil))
		done <- rec
	}()

	select {
	case rec := <-done:
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("readiness handler did not return within 2s of a hanging checker — timeout bound is broken")
	}
}
