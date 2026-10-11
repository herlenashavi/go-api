package main

import (
	"context"

	villainsv1 "github.com/herlenashavi/go-api/examples/villains/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// villainStore is everything VillainServer needs from a store — this
// package's analogue of examples/heroes's heroStore (retire.go). Both
// VillainStore (store.go, in-memory) and VillainPGStore (store_pg.go,
// real Postgres) implement it, so main.go can construct either one
// without anything here caring which: every method returns an error
// (even VillainStore's, which can't actually fail) so a real backend
// has somewhere to report one.
type villainStore interface {
	List(ctx context.Context, statusFilter villainsv1.VillainStatus, limit, offset int) ([]Villain, int, error)
	Get(ctx context.Context, id string) (Villain, error)
	Create(ctx context.Context, name, evilPower, nemesisHeroID string, threatLevel villainsv1.ThreatLevel) (Villain, error)
	Capture(ctx context.Context, id string) (Villain, error)
}

// VillainServer implements villainsv1.VillainServiceServer. Each method
// follows the same shape: authorize first (using the Principal
// grpcx's server interceptor already placed in ctx), then touch the
// store, then map any error through grpcx.Status so it comes back as the
// right gRPC code instead of an undifferentiated Internal.
//
// There is deliberately no generic "authorize this RPC" interceptor (see
// pkg/grpcx's package doc) — authorize() below is this service's own
// version of the same inline-call pattern examples/heroes's hand-written
// routes and pkg/crud's generated ones both use.
type VillainServer struct {
	villainsv1.UnimplementedVillainServiceServer
	store      villainStore
	authorizer *authz.Authorizer
}

func NewVillainServer(store villainStore, authorizer *authz.Authorizer) *VillainServer {
	return &VillainServer{store: store, authorizer: authorizer}
}

func (s *VillainServer) authorize(ctx context.Context, action authz.Action, resourceID string) error {
	principal, ok := authn.FromContext(ctx)
	if !ok || principal == nil {
		return grpcx.Status(problem.Unauthorized(""))
	}
	ref := authz.CollectionResource("Villain")
	if resourceID != "" {
		ref = authz.ResourceRef{Type: "Villain", ID: resourceID}
	}
	if err := s.authorizer.Authorize(ctx, principal, action, ref, nil); err != nil {
		return grpcx.Status(err)
	}
	return nil
}

func (s *VillainServer) ListVillains(ctx context.Context, req *villainsv1.ListVillainsRequest) (*villainsv1.ListVillainsResponse, error) {
	if err := s.authorize(ctx, "villain:list", ""); err != nil {
		return nil, err
	}

	items, total, err := s.store.List(ctx, req.StatusFilter, int(req.Limit), int(req.Offset))
	if err != nil {
		return nil, grpcx.Status(err)
	}
	resp := &villainsv1.ListVillainsResponse{Total: int32(total)}
	for _, v := range items {
		resp.Villains = append(resp.Villains, v.toProto())
	}
	return resp, nil
}

func (s *VillainServer) GetVillain(ctx context.Context, req *villainsv1.GetVillainRequest) (*villainsv1.GetVillainResponse, error) {
	if err := s.authorize(ctx, "villain:read", req.Id); err != nil {
		return nil, err
	}

	v, err := s.store.Get(ctx, req.Id)
	if err != nil {
		return nil, grpcx.Status(err)
	}
	return &villainsv1.GetVillainResponse{Villain: v.toProto()}, nil
}

func (s *VillainServer) CreateVillain(ctx context.Context, req *villainsv1.CreateVillainRequest) (*villainsv1.CreateVillainResponse, error) {
	if err := s.authorize(ctx, "villain:create", ""); err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, grpcx.Status(problem.Validation(problem.FieldError{Field: "name", Detail: "is required"}))
	}

	v, err := s.store.Create(ctx, req.Name, req.EvilPower, req.NemesisHeroId, villainsv1.ThreatLevel_THREAT_LEVEL_UNSPECIFIED)
	if err != nil {
		return nil, grpcx.Status(err)
	}
	return &villainsv1.CreateVillainResponse{Villain: v.toProto()}, nil
}

// CaptureVillain does a single, row-level (ABAC) authorize call rather
// than authorize()'s coarse (type, id)-only check: it loads the villain
// first and attaches its real `status`, so policies/villains.cedar's
// "can't capture an already-captured villain" forbid clause has
// something to evaluate — the same pattern examples/heroes's retire.go
// uses for the identical reason. A coarse check first, then this one,
// would just evaluate the policy set twice for the same decision.
func (s *VillainServer) CaptureVillain(ctx context.Context, req *villainsv1.CaptureVillainRequest) (*villainsv1.CaptureVillainResponse, error) {
	principal, ok := authn.FromContext(ctx)
	if !ok || principal == nil {
		return nil, grpcx.Status(problem.Unauthorized(""))
	}

	existing, err := s.store.Get(ctx, req.Id)
	if err != nil {
		return nil, grpcx.Status(err)
	}

	resource := authz.ResourceRef{Type: "Villain", ID: req.Id}
	attrs := authz.Attrs(map[string]any{"status": shortStatus(existing.Status)})
	if err := s.authorizer.Authorize(ctx, principal, "villain:capture", resource, attrs); err != nil {
		return nil, grpcx.Status(err)
	}

	v, err := s.store.Capture(ctx, req.Id)
	if err != nil {
		return nil, grpcx.Status(err)
	}
	return &villainsv1.CaptureVillainResponse{Villain: v.toProto()}, nil
}
