package authz

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// Route declares what a single route needs authorized: which action it
// performs, and how to find the resource it acts on. Pair it with
// Middleware to protect a handwritten chi route; crud.Resource derives this
// automatically for the generated CRUD endpoints.
type Route struct {
	// Action is the Cedar action this route performs, e.g. "heroes:update".
	Action Action
	// ResourceType is the Cedar entity type for the resource, e.g. "Hero".
	ResourceType string
	// IDParam is the chi URL parameter holding the resource's ID (e.g.
	// "id" for a route registered as "/heroes/{id}"). Leave empty for
	// collection-level actions (list, create) — the resource ID becomes
	// "*", per CollectionResource.
	IDParam string
}

// Middleware enforces r against every request, using the Principal that
// authn.Middleware must have already placed in context (mount authz
// middleware after authn middleware in the chain). On denial it writes a
// problem+json response and does not call the next handler.
//
// This performs the coarse, route-level check only: principal + action +
// (resource type, resource id). It cannot see the resource's own data (its
// owner, its tenant, its status), so a policy that depends on those must be
// enforced again inside the handler via Authorizer.Check once the row is
// loaded — Middleware is the first gate, not necessarily the only one.
func Middleware(a *Authorizer, route Route) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := authn.FromContext(r.Context())
			if !ok || principal == nil {
				problem.Write(w, r, problem.Unauthorized(""))
				return
			}

			resource := CollectionResource(route.ResourceType)
			if route.IDParam != "" {
				if id := chi.URLParam(r, route.IDParam); id != "" {
					resource = ResourceRef{Type: route.ResourceType, ID: id}
				}
			}

			if err := a.Authorize(r.Context(), principal, route.Action, resource, nil); err != nil {
				problem.Write(w, r, problem.From(err, a.Debug))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
