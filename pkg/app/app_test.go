package app_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/config"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
)

func testConfig() config.Base {
	return config.Base{
		ServiceName:            "app-test",
		Env:                    "test",
		HTTPHost:               "127.0.0.1",
		HTTPPort:               0, // OS-assigned; see App.Addr
		LogLevel:               "error",
		ShutdownTimeoutSeconds: 5,
		RequestTimeoutSeconds:  5,
		MaxBodyBytes:           1 << 20,
	}
}

func TestApp_HealthAndMountedRoutes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secret := []byte("test-secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})

	a, err := app.New(ctx, app.Options{Config: testConfig(), Verifier: verifier})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	protected := chi.NewRouter()
	protected.Get("/", func(w http.ResponseWriter, r *http.Request) {
		p, ok := authn.FromContext(r.Context())
		if !ok {
			t.Error("expected a principal in context on a Mount-ed route")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(p.Subject))
	})
	a.Mount("/v1/secure", protected)

	public := chi.NewRouter()
	public.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("public"))
	})
	a.MountPublic("/v1/open", public)

	done := make(chan error, 1)
	go func() { done <- a.ListenAndServe(ctx) }()

	baseURL := waitForAddr(t, a)

	// Liveness is reachable with no auth.
	mustGet(t, baseURL+"/health/liveness", http.StatusOK)

	// Public mount needs no token.
	mustGet(t, baseURL+"/v1/open/", http.StatusOK)

	// Protected mount rejects missing token.
	mustGet(t, baseURL+"/v1/secure/", http.StatusUnauthorized)

	// Protected mount accepts a valid token and the handler sees the principal.
	token, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice"}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/secure/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/secure: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	// Graceful shutdown: canceling ctx should make ListenAndServe return nil
	// promptly, not hang until ShutdownTimeoutSeconds.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after context cancellation")
	}
}

// TestApp_GRPCEnabled proves the gRPC server shares App's lifecycle: it
// starts alongside HTTP, serves real RPCs (the built-in health service,
// registered automatically from GRPCHealthChecks), and stops gracefully
// on the same context cancellation — without the test needing to touch
// pkg/grpcx directly.
func TestApp_GRPCEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := testConfig()
	a, err := app.New(ctx, app.Options{
		Config:      cfg,
		GRPCEnabled: true,
		GRPCHost:    "127.0.0.1",
		GRPCPort:    0, // OS-assigned; see App.GRPCAddr
		GRPCHealthChecks: []health.Check{
			{Name: "ok", Checker: func(context.Context) error { return nil }},
		},
	})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	if a.GRPCServer == nil {
		t.Fatal("expected App.GRPCServer to be set when GRPCEnabled is true")
	}

	done := make(chan error, 1)
	go func() { done <- a.ListenAndServe(ctx) }()

	_ = waitForAddr(t, a) // ensures ListenAndServe has started before we probe GRPCAddr
	grpcAddr := waitForGRPCAddr(t, a)

	conn, err := grpcx.Dial(grpcAddr, grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("grpcx.Dial: %v", err)
	}
	defer conn.Close()

	checkCtx, checkCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer checkCancel()
	resp, err := healthpb.NewHealthClient(conn).Check(checkCtx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("grpc health Check: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.Status)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error on shutdown: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after context cancellation (gRPC GracefulStop hung?)")
	}
}

func waitForGRPCAddr(t *testing.T, a *app.App) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := a.GRPCAddr(); addr != "" {
			return addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never reported a grpc listen address")
	return ""
}

func waitForAddr(t *testing.T, a *app.App) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if addr := a.Addr(); addr != "" {
			return "http://" + addr
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("server never reported a listen address")
	return ""
}

func mustGet(t *testing.T, url string, wantStatus int) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s: status = %d, want %d", url, resp.StatusCode, wantStatus)
	}
}
