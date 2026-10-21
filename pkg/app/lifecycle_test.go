package app_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"github.com/herlenashavi/go-api/pkg/app"
	"github.com/herlenashavi/go-api/pkg/grpcx"
)

// TestApp_ServerStoppingStopsTheOther: when one server's Serve returns on
// its own, ListenAndServe used to return immediately with the other server
// still serving and telemetry unflushed. Stopping the gRPC server out from
// under it must now take HTTP down too.
func TestApp_ServerStoppingStopsTheOther(t *testing.T) {
	cfg := testConfig()
	cfg.GRPCHost = "127.0.0.1"
	cfg.GRPCPort = 0
	a, err := app.New(context.Background(), app.Options{Config: cfg, GRPCEnabled: true})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.ListenAndServe(context.Background()) }()
	_ = waitForAddr(t, a)
	_ = waitForGRPCAddr(t, a)

	a.GRPCServer.Stop()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after the gRPC server stopped")
	}
	if conn, err := net.DialTimeout("tcp", a.Addr(), time.Second); err == nil {
		conn.Close()
		t.Fatalf("HTTP server at %s is still accepting connections after ListenAndServe returned", a.Addr())
	}
}

// TestApp_ReadTimeoutCutsOffATricklingBody: HTTP_READ_TIMEOUT_SECONDS must
// actually reach the http.Server — a client that sends headers and then
// trickles (here: stalls) its body has to be cut off at the read timeout,
// not left holding a connection and goroutine until RequestTimeoutSeconds.
func TestApp_ReadTimeoutCutsOffATricklingBody(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := testConfig()
	cfg.HTTPReadTimeoutSeconds = 1
	cfg.RequestTimeoutSeconds = 10
	a, err := app.New(ctx, app.Options{Config: cfg})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	upload := chi.NewRouter()
	upload.Post("/", func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.ReadAll(r.Body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	a.MountPublic("/v1/upload", upload)

	go func() { _ = a.ListenAndServe(ctx) }()
	_ = waitForAddr(t, a)

	conn, err := net.Dial("tcp", a.Addr())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	// Promise 1000 bytes, send 5, then stall.
	_, _ = conn.Write([]byte("POST /v1/upload/ HTTP/1.1\r\nHost: x\r\nContent-Length: 1000\r\n\r\nhello"))

	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _ = io.ReadAll(conn) // returns once the server responds and closes, or at our 5s deadline
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("server held the stalled upload for %v; want it cut off near the 1s read timeout", elapsed)
	}
}

// TestApp_GRPCListenFailureStopsHTTP: if the gRPC listener can't bind
// after HTTP is already serving, ListenAndServe must return an error *and*
// take the HTTP server down with it.
func TestApp_GRPCListenFailureStopsHTTP(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer taken.Close()

	cfg := testConfig()
	cfg.GRPCHost = "127.0.0.1"
	cfg.GRPCPort = taken.Addr().(*net.TCPAddr).Port
	a, err := app.New(context.Background(), app.Options{Config: cfg, GRPCEnabled: true})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- a.ListenAndServe(context.Background()) }()

	var lsErr error
	select {
	case lsErr = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after the gRPC listen failure")
	}
	if lsErr == nil {
		t.Fatal("ListenAndServe returned nil, want the gRPC listen error")
	}

	httpAddr := a.Addr()
	if httpAddr == "" {
		t.Fatal("HTTP server never started; the test isn't exercising the intended path")
	}
	if conn, err := net.DialTimeout("tcp", httpAddr, time.Second); err == nil {
		conn.Close()
		t.Fatalf("HTTP server at %s is still accepting connections after ListenAndServe returned", httpAddr)
	}
}

// TestApp_HTTPDrainTimeoutStillStopsGRPC: an HTTP drain that overruns
// ShutdownTimeoutSeconds used to return early, skipping the gRPC stop and
// the telemetry flush. Every step must still run.
func TestApp_HTTPDrainTimeoutStillStopsGRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := testConfig()
	cfg.ShutdownTimeoutSeconds = 0 // any in-flight request overruns the drain
	cfg.GRPCHost = "127.0.0.1"
	cfg.GRPCPort = 0
	a, err := app.New(ctx, app.Options{Config: cfg, GRPCEnabled: true})
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	release := make(chan struct{})
	defer close(release)
	entered := make(chan struct{})
	slow := chi.NewRouter()
	slow.Get("/", func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	a.MountPublic("/v1/slow", slow)

	done := make(chan error, 1)
	go func() { done <- a.ListenAndServe(ctx) }()
	baseURL := waitForAddr(t, a)
	grpcAddr := waitForGRPCAddr(t, a)

	go func() {
		resp, err := http.Get(baseURL + "/v1/slow/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ListenAndServe returned nil, want the drain-timeout error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ListenAndServe did not return after cancellation")
	}

	conn, err := grpcx.Dial(grpcAddr, grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("grpcx.Dial: %v", err)
	}
	defer conn.Close()
	checkCtx, checkCancel := context.WithTimeout(context.Background(), time.Second)
	defer checkCancel()
	if _, err := healthpb.NewHealthClient(conn).Check(checkCtx, &healthpb.HealthCheckRequest{}); err == nil {
		t.Fatal("gRPC server still serving after an HTTP drain timeout")
	}
}
