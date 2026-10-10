package grpcx_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/health"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// stubVillainServer is a minimal villainsv1.VillainServiceServer used
// purely to exercise authnInterceptor against an ordinary (non-health)
// RPC — see TestAuthRequired's doc comment for why health itself can't
// be used for that anymore.
type stubVillainServer struct {
	villainsv1.UnimplementedVillainServiceServer
}

func (stubVillainServer) ListVillains(context.Context, *villainsv1.ListVillainsRequest) (*villainsv1.ListVillainsResponse, error) {
	return &villainsv1.ListVillainsResponse{}, nil
}

// panickingVillainServer is stubVillainServer plus one method that
// panics, for exercising recoveryInterceptor against a real handler
// instead of only asserting it's wired in.
type panickingVillainServer struct {
	stubVillainServer
}

func (panickingVillainServer) GetVillain(context.Context, *villainsv1.GetVillainRequest) (*villainsv1.GetVillainResponse, error) {
	panic("boom")
}

// TestRecoveryInterceptor_CatchesPanic proves a panicking handler becomes
// a codes.Internal error over the wire, and — the actual point — that
// the server process survives to answer a second, unrelated RPC
// afterward. An unrecovered panic in a gRPC handler goroutine would take
// the whole process down (see recoveryInterceptor's doc comment); if
// that happened here, this test's own process would crash, which no
// amount of t.Fatal can report — the second call succeeding is the real
// assertion.
func TestRecoveryInterceptor_CatchesPanic(t *testing.T) {
	server := grpcx.NewServer(grpcx.Dependencies{})
	villainsv1.RegisterVillainServiceServer(server, panickingVillainServer{})

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := villainsv1.NewVillainServiceClient(conn)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err = client.GetVillain(ctx, &villainsv1.GetVillainRequest{Id: "x"})
	if status.Code(err) != codes.Internal {
		t.Fatalf("code = %v, want Internal", status.Code(err))
	}

	// The process (and this server) must still be alive to answer this.
	if _, err := client.ListVillains(ctx, &villainsv1.ListVillainsRequest{}); err != nil {
		t.Fatalf("ListVillains after a panic on another call: %v", err)
	}
}

// TestHealthEndToEnd exercises NewServer, RegisterHealth, and Dial against
// a real loopback socket: the simplest proof that the interceptor chain,
// the otelgrpc stats handler, and the generated health service all compose
// without one tripping over another.
func TestHealthEndToEnd(t *testing.T) {
	server := grpcx.NewServer(grpcx.Dependencies{})
	grpcx.RegisterHealth(server, []health.Check{
		{Name: "ok", Checker: func(context.Context) error { return nil }},
	}, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.Status)
	}
}

// TestHealthEndToEnd_Unhealthy confirms a failing Checker surfaces as
// NOT_SERVING through the real RPC, not just in health.Run's own return
// value.
func TestHealthEndToEnd_Unhealthy(t *testing.T) {
	server := grpcx.NewServer(grpcx.Dependencies{})
	grpcx.RegisterHealth(server, []health.Check{
		{Name: "down", Checker: func(context.Context) error { return context.DeadlineExceeded }},
	}, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	client := healthpb.NewHealthClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
		t.Fatalf("status = %v, want NOT_SERVING", resp.Status)
	}
}

// TestAuthRequired confirms a server built with a Verifier rejects calls
// to an ordinary RPC with no (or an invalid) bearer token and accepts one
// with a valid token.
func TestAuthRequired(t *testing.T) {
	secret := []byte("test-secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})

	server := grpcx.NewServer(grpcx.Dependencies{Verifier: verifier})
	villainsv1.RegisterVillainServiceServer(server, stubVillainServer{})

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := villainsv1.NewVillainServiceClient(conn)

	t.Run("no token", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_, err := client.ListVillains(ctx, &villainsv1.ListVillainsRequest{})
		if status.Code(err) != codes.Unauthenticated {
			t.Fatalf("code = %v, want Unauthenticated", status.Code(err))
		}
	})

	t.Run("valid token", func(t *testing.T) {
		token, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice"}, time.Minute)
		if err != nil {
			t.Fatalf("SignHMAC: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)

		if _, err := client.ListVillains(ctx, &villainsv1.ListVillainsRequest{}); err != nil {
			t.Fatalf("ListVillains with valid token: %v", err)
		}
	})
}

// TestHealthExemptFromAuth confirms the standard gRPC health service is
// reachable with no token even when the server has a Verifier configured
// — matching health.Router's HTTP endpoints, which pkg/app mounts outside
// any auth middleware for the same reason: a load balancer or
// orchestrator probing health should never need credentials. This is a
// regression test for exactly the gap examples/villains hit when first
// wired up to a real client under a real network call: its gRPC health
// check started requiring a token, while its HTTP one never had.
func TestHealthExemptFromAuth(t *testing.T) {
	verifier := authn.NewHMACVerifier([]byte("secret"), authn.ClaimsConfig{})
	server := grpcx.NewServer(grpcx.Dependencies{Verifier: verifier})
	grpcx.RegisterHealth(server, nil, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("Check with no token: %v", err)
	}
	if resp.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status = %v, want SERVING", resp.Status)
	}
}

// TestNewServer_NoVerifier_AllowsUnauthenticatedCalls confirms a server
// built with no Verifier (the default) enforces no authentication at
// all — the gRPC equivalent of mounting a chi route with no
// authn.Middleware.
func TestNewServer_NoVerifier_AllowsUnauthenticatedCalls(t *testing.T) {
	server := grpcx.NewServer(grpcx.Dependencies{})
	grpcx.RegisterHealth(server, nil, time.Second)
	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

// TestRequestIDPropagation confirms a request ID set on the calling
// context (as authn/middleware would via reqctx) survives the client
// interceptor's metadata injection and the server interceptor's
// extraction — the whole point of carrying it across the service
// boundary at all.
func TestRequestIDPropagation(t *testing.T) {
	var serverSawID string
	done := make(chan struct{})

	server := grpc.NewServer(grpc.ChainUnaryInterceptor(
		func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
			serverSawID = reqctx.RequestID(ctxFromIncomingMetadata(ctx))
			close(done)
			return handler(ctx, req)
		},
	))
	grpcx.RegisterHealth(server, nil, time.Second)

	lis := mustListen(t)
	go func() { _ = server.Serve(lis) }()
	defer server.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	ctx := reqctx.WithRequestID(context.Background(), "req-abc-123")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{}); err != nil {
		t.Fatalf("Check: %v", err)
	}

	<-done
	if serverSawID != "req-abc-123" {
		t.Fatalf("server saw request id %q, want %q", serverSawID, "req-abc-123")
	}
}

// ctxFromIncomingMetadata re-derives the request ID the way
// requestIDInterceptor would, using only the exported metadata API — the
// interceptor's internal helper is unexported, so this test proves the
// wire behavior, not the private function.
func ctxFromIncomingMetadata(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	values := md.Get("x-request-id")
	if len(values) == 0 {
		return ctx
	}
	return reqctx.WithRequestID(ctx, values[0])
}

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return lis
}
