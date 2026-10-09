package main

import (
	"context"

	"github.com/google/uuid"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
	"github.com/herlenashavi/go-api/pkg/problem"
)

type retireInput struct {
	ID uuid.UUID `path:"id"`
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
func retireHandler(store *MemoryHeroStore, authorizer *authz.Authorizer, debug bool) func(ctx context.Context, in *retireInput) (*Hero, error) {
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

		updated, err := store.Retire(ctx, in.ID)
		if err != nil {
			return nil, err
		}
		return &updated, nil
	}
}
