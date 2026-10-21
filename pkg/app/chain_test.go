package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/app"
)

// TestApp_TimedOutResponseKeepsSecurityAndCORSHeaders pins the chain
// order: CORS and SecurityHeaders must run before Timeout, otherwise their
// headers are written into Timeout's buffer — which a timed-out request
// discards — and the 503 goes out without them (a browser then reports a
// CORS failure instead of the timeout).
func TestApp_TimedOutResponseKeepsSecurityAndCORSHeaders(t *testing.T) {
	cfg := testConfig()
	cfg.RequestTimeoutSeconds = 1
	cfg.CORSAllowedOrigins = []string{"https://app.example"}

	a, err := app.New(context.Background(), app.Options{Config: cfg})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	slow := chi.NewRouter()
	slow.Get("/", func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // outlive the 1s request timeout
	})
	a.MountPublic("/v1/slow", slow)

	req := httptest.NewRequest(http.MethodGet, "/v1/slow/", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 from the timeout middleware", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q on the timeout response, want nosniff", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q on the timeout response, want the allowed origin", got)
	}
}
