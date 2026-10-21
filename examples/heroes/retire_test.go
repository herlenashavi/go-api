package main

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/problem"
)

func TestMemoryHeroStore_RetireIsConditionalOnStatus(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryHeroStore()
	h, err := s.Create(ctx, CreateHero{Name: "Clark", SuperPower: "flight"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = s.Retire(ctx, h.ID, HeroStatusInactive)
	if p := problem.From(err, true); err == nil || p.Status != 409 {
		t.Fatalf("stale expected status: err = %v, want 409", err)
	}
	if got, _ := s.Get(ctx, h.ID.String()); got.Status != HeroStatusActive {
		t.Fatalf("status = %q after a rejected retire, want unchanged %q", got.Status, HeroStatusActive)
	}

	retired, err := s.Retire(ctx, h.ID, HeroStatusActive)
	if err != nil {
		t.Fatalf("Retire: %v", err)
	}
	if retired.Status != HeroStatusRetired {
		t.Fatalf("status = %q, want retired", retired.Status)
	}

	_, err = s.Retire(ctx, uuid.New(), HeroStatusActive)
	if p := problem.From(err, true); err == nil || p.Status != 404 {
		t.Fatalf("unknown hero: err = %v, want 404", err)
	}
}

// racingStore simulates a concurrent update landing between
// retireHandler's Get (which the authorization decision is based on) and
// its Retire call.
type racingStore struct {
	*MemoryHeroStore
}

func (s racingStore) Get(ctx context.Context, id string) (Hero, error) {
	h, err := s.MemoryHeroStore.Get(ctx, id)
	if err != nil {
		return h, err
	}
	inactive := HeroStatusInactive
	if _, err := s.MemoryHeroStore.Update(ctx, id, UpdateHero{Status: &inactive}); err != nil {
		return Hero{}, err
	}
	return h, nil // the pre-update snapshot, as a real race would see it
}

func TestRetireHandler_StatusChangedAfterAuthorizationIsConflict(t *testing.T) {
	ctx := authn.WithPrincipal(context.Background(), &authn.Principal{Subject: "alice", Roles: []string{"admin"}})
	mem := NewMemoryHeroStore()
	h, err := mem.Create(context.Background(), CreateHero{Name: "Clark", SuperPower: "flight"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = retireHandler(racingStore{mem}, nil, true)(ctx, &retireInput{ID: h.ID})
	if p := problem.From(err, true); err == nil || p.Status != 409 {
		t.Fatalf("err = %v, want 409", err)
	}
}
