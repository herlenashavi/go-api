package health_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestRouter_ReadinessHidesCheckErrors: /health/readiness needs no
// credentials, so a failing check's error text — which for a database
// ping names hosts, users, and databases — must go to the log, not the
// response body.
func TestRouter_ReadinessHidesCheckErrors(t *testing.T) {
	const secret = "failed to connect to `user=app database=heroes host=10.0.0.5`"
	var logBuf bytes.Buffer
	r := health.Router(health.Config{
		Logger: slog.New(slog.NewJSONHandler(&logBuf, nil)),
		Checks: []health.Check{
			{Name: "database", Checker: func(ctx context.Context) error { return errors.New(secret) }},
		},
	})

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readiness", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "user=app") {
		t.Fatalf("readiness body leaks the check error: %s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"name":"database"`) {
		t.Fatalf("readiness body should still name the failing check: %s", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "database=heroes") {
		t.Fatalf("the check error should be logged server-side; log = %s", logBuf.String())
	}
}

// TestRouter_ReadinessBoundsHangingChecker is the regression test for a
// Checker that ignores context cancellation and blocks forever: it must
// not be able to hang the readiness endpoint past PerCheckTimeout.
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
