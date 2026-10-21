package main

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/grpcx"
)

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return lis
}

// newTestServer wires the exact pieces main.go's run() does (roles,
// the real shipped policies/villains.cedar, a fresh store), minus the
// network listener — tests call the VillainServer methods directly,
// with a Principal placed in context the way grpcx's authn interceptor
// would for a real RPC.
func newTestServer(t *testing.T) *VillainServer {
	t.Helper()
	roles, err := authz.NewRoleRegistry(
		authz.Role{Name: "viewer"},
		authz.Role{Name: "editor", Parents: []string{"viewer"}},
		authz.Role{Name: "admin", Parents: []string{"editor"}},
	)
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}
	policyStore, err := authz.LoadPolicyDir("./policies")
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	authorizer := authz.New(policyStore, roles, nil)
	return NewVillainServer(NewVillainStore(), authorizer)
}

func ctxAs(roles ...string) context.Context {
	return authn.WithPrincipal(context.Background(), &authn.Principal{Subject: "tester", Roles: roles})
}

func TestVillainServer_RBAC(t *testing.T) {
	s := newTestServer(t)

	if _, err := s.CreateVillain(ctxAs("viewer"), &villainsv1.CreateVillainRequest{Name: "Luthor"}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("viewer create: code = %v, want PermissionDenied", status.Code(err))
	}

	createResp, err := s.CreateVillain(ctxAs("editor"), &villainsv1.CreateVillainRequest{Name: "Luthor", EvilPower: "intellect"})
	if err != nil {
		t.Fatalf("editor create: %v", err)
	}
	id := createResp.Villain.Id

	if _, err := s.CaptureVillain(ctxAs("editor"), &villainsv1.CaptureVillainRequest{Id: id}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("editor capture: code = %v, want PermissionDenied", status.Code(err))
	}

	if _, err := s.CaptureVillain(ctxAs("admin"), &villainsv1.CaptureVillainRequest{Id: id}); err != nil {
		t.Fatalf("admin capture: %v", err)
	}
}

func TestVillainServer_CannotCaptureTwice(t *testing.T) {
	s := newTestServer(t)

	created, err := s.CreateVillain(ctxAs("admin"), &villainsv1.CreateVillainRequest{Name: "Zod"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.Villain.Id

	if _, err := s.CaptureVillain(ctxAs("admin"), &villainsv1.CaptureVillainRequest{Id: id}); err != nil {
		t.Fatalf("first capture: %v", err)
	}

	_, err = s.CaptureVillain(ctxAs("admin"), &villainsv1.CaptureVillainRequest{Id: id})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("second capture: code = %v, want PermissionDenied (already captured)", status.Code(err))
	}
}

func TestVillainServer_GetNotFound(t *testing.T) {
	s := newTestServer(t)
	_, err := s.GetVillain(ctxAs("viewer"), &villainsv1.GetVillainRequest{Id: "does-not-exist"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want NotFound", status.Code(err))
	}
}

func TestVillainServer_ListFiltersByStatus(t *testing.T) {
	s := newTestServer(t)

	for _, name := range []string{"Luthor", "Zod"} {
		if _, err := s.CreateVillain(ctxAs("admin"), &villainsv1.CreateVillainRequest{Name: name}); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	list, err := s.ListVillains(ctxAs("viewer"), &villainsv1.ListVillainsRequest{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list.Total != 2 {
		t.Fatalf("total = %d, want 2", list.Total)
	}

	active, err := s.ListVillains(ctxAs("viewer"), &villainsv1.ListVillainsRequest{
		StatusFilter: villainsv1.VillainStatus_VILLAIN_STATUS_CAPTURED,
	})
	if err != nil {
		t.Fatalf("list captured: %v", err)
	}
	if active.Total != 0 {
		t.Fatalf("total = %d, want 0 (none captured yet)", active.Total)
	}
}

// TestVillainServer_ListBoundsPaging covers ListVillainsRequest's
// client-supplied limit/offset: an oversized limit is capped at
// maxPageSize, and a negative offset is treated as 0 instead of slicing
// out of range.
func TestVillainServer_ListBoundsPaging(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < maxPageSize+5; i++ {
		if _, err := s.CreateVillain(ctxAs("admin"), &villainsv1.CreateVillainRequest{Name: fmt.Sprintf("v%03d", i)}); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	huge, err := s.ListVillains(ctxAs("viewer"), &villainsv1.ListVillainsRequest{Limit: 1 << 30})
	if err != nil {
		t.Fatalf("list with huge limit: %v", err)
	}
	if len(huge.Villains) != maxPageSize {
		t.Fatalf("len = %d, want %d (capped)", len(huge.Villains), maxPageSize)
	}
	if huge.Total != maxPageSize+5 {
		t.Fatalf("total = %d, want %d", huge.Total, maxPageSize+5)
	}

	negative, err := s.ListVillains(ctxAs("viewer"), &villainsv1.ListVillainsRequest{Limit: 3, Offset: -5})
	if err != nil {
		t.Fatalf("list with negative offset: %v", err)
	}
	if len(negative.Villains) != 3 || negative.Villains[0].Name != "v000" {
		t.Fatalf("negative offset: got %d villains starting %q, want 3 starting v000", len(negative.Villains), negative.Villains[0].GetName())
	}
}

func TestNormalizePage(t *testing.T) {
	cases := []struct{ limit, offset, wantLimit, wantOffset int }{
		{0, 0, defaultPageSize, 0},
		{-1, -1, defaultPageSize, 0},
		{50, 10, 50, 10},
		{maxPageSize + 1, 0, maxPageSize, 0},
	}
	for _, c := range cases {
		if l, o := normalizePage(c.limit, c.offset); l != c.wantLimit || o != c.wantOffset {
			t.Errorf("normalizePage(%d, %d) = (%d, %d), want (%d, %d)", c.limit, c.offset, l, o, c.wantLimit, c.wantOffset)
		}
	}
}

// TestEndToEndOverRealGRPC proves main.go's actual wiring — grpcx.NewServer
// with a Verifier, RegisterHealth, the real policy file — works over a
// real socket, not just via direct method calls.
func TestEndToEndOverRealGRPC(t *testing.T) {
	secret := []byte("test-secret")
	server := newTestServer(t)

	grpcServer := grpcx.NewServer(grpcx.Dependencies{
		Verifier: authn.NewHMACVerifier(secret, authn.ClaimsConfig{}),
	})
	villainsv1.RegisterVillainServiceServer(grpcServer, server)

	lis := mustListen(t)
	go func() { _ = grpcServer.Serve(lis) }()
	defer grpcServer.Stop()

	conn, err := grpcx.Dial(lis.Addr().String(), grpcx.Dependencies{})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	client := villainsv1.NewVillainServiceClient(conn)

	token, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice", Roles: []string{"admin"}}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)

	resp, err := client.CreateVillain(ctx, &villainsv1.CreateVillainRequest{Name: "Brainiac"})
	if err != nil {
		t.Fatalf("CreateVillain over grpc: %v", err)
	}
	if resp.Villain.Name != "Brainiac" {
		t.Fatalf("got %q, want Brainiac", resp.Villain.Name)
	}
}
