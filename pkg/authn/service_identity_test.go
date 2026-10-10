package authn_test

import (
	"context"
	"testing"

	"github.com/herlenashavi/go-api/pkg/authn"
)

func TestServiceIdentity_HasRole(t *testing.T) {
	s := &authn.ServiceIdentity{Name: "heroes", Roles: []string{"caller", "reader"}}
	if !s.HasRole("caller") || !s.HasRole("reader") {
		t.Fatalf("expected roles to be present, got %v", s.Roles)
	}
	if s.HasRole("admin") {
		t.Fatal("expected HasRole to be false for an ungranted role")
	}

	var nilIdentity *authn.ServiceIdentity
	if nilIdentity.HasRole("anything") {
		t.Fatal("expected HasRole on a nil *ServiceIdentity to be false, not panic")
	}
}

func TestServiceIdentityFromContext(t *testing.T) {
	if _, ok := authn.ServiceIdentityFromContext(context.Background()); ok {
		t.Fatal("expected no ServiceIdentity on a bare context")
	}

	s := &authn.ServiceIdentity{Name: "villains"}
	ctx := authn.WithServiceIdentity(context.Background(), s)
	got, ok := authn.ServiceIdentityFromContext(ctx)
	if !ok || got.Name != "villains" {
		t.Fatalf("got %+v, ok=%v, want Name=%q", got, ok, "villains")
	}
}

func TestServiceLogFields(t *testing.T) {
	if fields := authn.ServiceLogFields(context.Background()); fields != nil {
		t.Fatalf("expected no fields on a bare context, got %v", fields)
	}

	ctx := authn.WithServiceIdentity(context.Background(), &authn.ServiceIdentity{Name: "villains"})
	fields := authn.ServiceLogFields(ctx)
	if len(fields) != 1 || fields[0].Key != "calling_service" || fields[0].Value.String() != "villains" {
		t.Fatalf("got %v, want a single calling_service=villains attr", fields)
	}
}
