package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/crud"
	"github.com/herlenashavi/go-api/pkg/problem"
)

type retireInput struct {
	ID uuid.UUID `path:"id"`
}

// heroStore is everything main.go's hand-written routes need beyond the
// generic crud.Store[Hero, CreateHero, UpdateHero] interface. Retire
// isn't part of that interface — see MemoryHeroStore.Retire's doc
// comment on why it's a method on the concrete store instead — so both
// backends (MemoryHeroStore and PGHeroStore) implement it here too,
// which is what lets retireHandler and nemesisHandler work unchanged
// whichever one main.go constructs.
type heroStore interface {
	crud.Store[Hero, CreateHero, UpdateHero]
	// Retire retires hero id only if its status is still expected —
	// the status the caller authorized against. A mismatch is a 409
	// problem.Conflict; see retireHandler.
	Retire(ctx context.Context, id uuid.UUID, expected HeroStatus) (Hero, error)
}

// heroStatusChanged is the 409 both stores' Retire return when the hero's
// status no longer matches the one retireHandler authorized against. A
// constructor, not a shared var: problem.Write fills in per-request fields
// (Instance, RequestID) on the *Problem it's given.
func heroStatusChanged() *problem.Problem {
	return problem.Conflict("The hero's status changed while this request was being processed; retry.")
}

// retireHandler is a hand-written route — the thing crud.Resource is
// explicitly not trying to be the only way to build. It demonstrates two
// things the generic CRUD routes can't:
//
//  1. An action ("retire") that isn't one of list/get/create/update/delete.
//  2. Row-level ABAC: it loads the hero first, then calls
//     Authorizer.Check directly with the hero's *actual* status attached,
//     so the policy's `resource.status == "retired"` forbid clause has
//     something to evaluate. Resource's route-level checks never have
//     this — they run before any row is loaded.
//
// Loading, authorizing, then writing is a read-check-write sequence, so
// the write is conditional on the status the decision was made against:
// if a concurrent request changed it in between, Retire returns 409
// rather than acting on an authorization decision that no longer holds.
func retireHandler(store heroStore, authorizer *authz.Authorizer, debug bool) func(ctx context.Context, in *retireInput) (*Hero, error) {
	return func(ctx context.Context, in *retireInput) (*Hero, error) {
		principal, ok := authn.FromContext(ctx)
		if !ok || principal == nil {
			return nil, problem.Unauthorized("")
		}

		hero, err := store.Get(ctx, in.ID.String())
		if err != nil {
			return nil, err
		}

		resource := authz.ResourceRef{Type: "Hero", ID: in.ID.String()}
		attrs := authz.Attrs(map[string]any{"status": string(hero.Status)})
		if authorizer != nil {
			if err := authorizer.Authorize(ctx, principal, "hero:retire", resource, attrs); err != nil {
				return nil, err
			}
		}

		updated, err := store.Retire(ctx, in.ID, hero.Status)
		if err != nil {
			return nil, err
		}
		return &updated, nil
	}
}
