package main

import (
	"context"

	villainsv1 "github.com/herlenashavi/go-api/gen/villains/v1"
	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/grpcx"
	"github.com/herlenashavi/go-api/pkg/problem"
)

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
	store      *VillainStore
	authorizer *authz.Authorizer
}

func NewVillainServer(store *VillainStore, authorizer *authz.Authorizer) *VillainServer {
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

	items, total := s.store.List(req.StatusFilter, int(req.Limit), int(req.Offset))
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

	v, ok := s.store.Get(req.Id)
	if !ok {
		return nil, grpcx.Status(problem.NotFound("Villain"))
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

	v := s.store.Create(req.Name, req.EvilPower, req.NemesisHeroId, villainsv1.ThreatLevel_THREAT_LEVEL_UNSPECIFIED)
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

	existing, ok := s.store.Get(req.Id)
	if !ok {
		return nil, grpcx.Status(problem.NotFound("Villain"))
	}

	resource := authz.ResourceRef{Type: "Villain", ID: req.Id}
	attrs := authz.Attrs(map[string]any{"status": shortStatus(existing.Status)})
	if err := s.authorizer.Authorize(ctx, principal, "villain:capture", resource, attrs); err != nil {
		return nil, grpcx.Status(err)
	}

	v, ok := s.store.Capture(req.Id)
	if !ok {
		return nil, grpcx.Status(problem.NotFound("Villain"))
	}
	return &villainsv1.CaptureVillainResponse{Villain: v.toProto()}, nil
}
