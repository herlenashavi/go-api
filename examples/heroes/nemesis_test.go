package main

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	villainsv1 "github.com/herlenashavi/go-api/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/problem"
	"github.com/herlenashavi/go-api/pkg/reqctx"
)

// stubVillainServer is a minimal, protocol-conformant stand-in for
// examples/villains's real server — this test is about proving heroes's
// client-side wiring (VillainsClient, request-ID/token forwarding,
// error mapping) works against *any* correctly-behaving VillainService,
// not about re-testing villains's own logic (that's
// examples/villains/main_test.go's job; a main package can't be imported
// from another package, so sharing the real implementation isn't an
// option anyway — see this repo's PR history for why).
type stubVillainServer struct {
	villainsv1.UnimplementedVillainServiceServer
	villains map[string]*villainsv1.Villain

	lastAuthHeader string
	lastRequestID  string
}

func (s *stubVillainServer) GetVillain(ctx context.Context, req *villainsv1.GetVillainRequest) (*villainsv1.GetVillainResponse, error) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get("authorization"); len(v) > 0 {
			s.lastAuthHeader = v[0]
		}
		if v := md.Get("x-request-id"); len(v) > 0 {
			s.lastRequestID = v[0]
		}
	}
	v, ok := s.villains[req.Id]
	if !ok {
		return nil, status.Error(codes.NotFound, "villain not found")
	}
	return &villainsv1.GetVillainResponse{Villain: v}, nil
}

func startStubVillains(t *testing.T) (*stubVillainServer, string) {
	t.Helper()
	stub := &stubVillainServer{villains: map[string]*villainsv1.Villain{
		"v1": {Id: "v1", Name: "Lex Luthor", EvilPower: "intellect", ThreatLevel: villainsv1.ThreatLevel_THREAT_LEVEL_HIGH, Status: villainsv1.VillainStatus_VILLAIN_STATUS_ACTIVE},
	}}

	server := grpc.NewServer()
	villainsv1.RegisterVillainServiceServer(server, stub)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	go func() { _ = server.Serve(lis) }()
	t.Cleanup(server.Stop)

	return stub, lis.Addr().String()
}

func TestNemesisHandler_NoNemesisSet(t *testing.T) {
	_, addr := startStubVillains(t)
	client, err := NewVillainsClient(addr, grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("NewVillainsClient: %v", err)
	}
	defer client.Close()

	store := NewMemoryHeroStore()
	created, err := store.Create(context.Background(), CreateHero{Name: "Clark", SuperPower: "flight"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	handler := nemesisHandler(store, client)
	out, err := handler(context.Background(), &nemesisInput{ID: created.ID})
	if err != nil {
		t.Fatalf("nemesisHandler: %v", err)
	}
	if out.Villain != nil {
		t.Fatalf("expected no villain, got %+v", out.Villain)
	}
}

func TestNemesisHandler_ForwardsRequestIDAndToken(t *testing.T) {
	stub, addr := startStubVillains(t)
	client, err := NewVillainsClient(addr, grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("NewVillainsClient: %v", err)
	}
	defer client.Close()

	store := NewMemoryHeroStore()
	created, err := store.Create(context.Background(), CreateHero{Name: "Clark", SuperPower: "flight", NemesisVillainID: "v1"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Simulate what middleware.RequestID + authn.Middleware would already
	// have put in context for a real inbound HTTP request.
	ctx := reqctx.WithRequestID(context.Background(), "req-xyz")
	ctx = authn.WithRawToken(ctx, "the-users-token")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	handler := nemesisHandler(store, client)
	out, err := handler(ctx, &nemesisInput{ID: created.ID})
	if err != nil {
		t.Fatalf("nemesisHandler: %v", err)
	}
	if out.Villain == nil || out.Villain.Name != "Lex Luthor" {
		t.Fatalf("expected Lex Luthor, got %+v", out.Villain)
	}
	if out.Villain.ThreatLevel != "high" || out.Villain.Status != "active" {
		t.Fatalf("expected lowercase enum rendering, got threatLevel=%q status=%q", out.Villain.ThreatLevel, out.Villain.Status)
	}

	if stub.lastRequestID != "req-xyz" {
		t.Fatalf("villains server saw request id %q, want %q", stub.lastRequestID, "req-xyz")
	}
	if stub.lastAuthHeader != "Bearer the-users-token" {
		t.Fatalf("villains server saw authorization %q, want %q", stub.lastAuthHeader, "Bearer the-users-token")
	}
}

func TestNemesisHandler_UnknownVillainBecomesProblem(t *testing.T) {
	_, addr := startStubVillains(t)
	client, err := NewVillainsClient(addr, grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("NewVillainsClient: %v", err)
	}
	defer client.Close()

	store := NewMemoryHeroStore()
	created, err := store.Create(context.Background(), CreateHero{Name: "Clark", SuperPower: "flight", NemesisVillainID: "does-not-exist"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	handler := nemesisHandler(store, client)
	_, err = handler(context.Background(), &nemesisInput{ID: created.ID})
	if err == nil {
		t.Fatal("expected an error for an unresolvable villain id")
	}
	p := problem.From(err, true)
	if p.Status != 404 {
		t.Fatalf("status = %d, want 404 (grpcx.Problem mapping codes.NotFound)", p.Status)
	}
}
