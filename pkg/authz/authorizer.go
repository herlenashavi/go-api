package authz

import (
	"context"
	"log/slog"

	cedar "github.com/cedar-policy/cedar-go"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/problem"
)

// Action is a Cedar action identifier, scoped per API as policy data
// rather than Go code: "heroes:list", "heroes:read", "heroes:update",
// etc. Define your own per resource; there is no fixed enum because the
// whole point is that new APIs add new actions without touching this
// package.
type Action string

// EntityTypeAction is the Cedar entity type used for actions.
const EntityTypeAction cedar.EntityType = "Action"

// ActionEntityUID returns the Cedar entity UID for an action.
func ActionEntityUID(action Action) cedar.EntityUID {
	return cedar.NewEntityUID(EntityTypeAction, cedar.String(string(action)))
}

// Authorizer evaluates Cedar policies for a given (principal, action,
// resource). It is the single integration point between go-api's RBAC
// building blocks (RoleRegistry, authn.Principal, authn.ServiceIdentity)
// and the cedar-go engine.
type Authorizer struct {
	store *PolicyStore
	roles *RoleRegistry
	log   *slog.Logger
	// Debug controls whether denial diagnostics (which policies fired) are
	// returned to the caller in problem.Detail. Keep false in production —
	// policy structure is information disclosure.
	Debug bool
}

// New builds an Authorizer. logger may be nil (defaults to slog.Default()).
func New(store *PolicyStore, roles *RoleRegistry, logger *slog.Logger) *Authorizer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Authorizer{store: store, roles: roles, log: logger}
}

// Decision mirrors cedar.Decision to keep cedar-go out of this package's
// exported surface for callers that don't need the Diagnostic.
type Decision bool

const (
	Allow Decision = true
	Deny  Decision = false
)

// actor is the generalized shape Check/CheckService both reduce to: an
// identity with a Cedar entity UID, the roles it was granted, and
// whatever attributes should be on its entity. authn.Principal (a user,
// via a bearer token) and authn.ServiceIdentity (a service, via mTLS)
// both become one of these — see checkEntity.
type actor struct {
	uid   cedar.EntityUID
	roles []string
	attrs cedar.RecordMap
	// logValue identifies the actor in denial logs ("alice" for a user's
	// subject, a service's Name for a ServiceIdentity).
	logValue string
}

func userActor(p *authn.Principal) actor {
	return actor{
		uid:      UserEntityUID(p.Subject),
		roles:    p.Roles,
		attrs:    cedar.RecordMap{"tenantId": cedar.String(p.TenantID)},
		logValue: p.Subject,
	}
}

func serviceActor(s *authn.ServiceIdentity) actor {
	return actor{
		uid:      ServiceEntityUID(s.Name),
		roles:    s.Roles,
		attrs:    cedar.RecordMap{},
		logValue: s.Name,
	}
}

// Check evaluates whether principal may perform action on resource, with
// resourceAttrs attached to the resource entity for ABAC policies (pass nil
// for coarse, RBAC-only checks where only the resource's type/id is known).
// It never returns an error for a Deny decision — only for a Cedar
// evaluation error (a malformed policy) — so callers must inspect the
// returned Decision, not just err.
func (a *Authorizer) Check(
	ctx context.Context,
	p *authn.Principal,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) (Decision, cedar.Diagnostic, error) {
	return a.checkEntity(ctx, userActor(p), action, resource, resourceAttrs)
}

// Authorize is Check plus the HTTP/gRPC-shaped error mapping: nil on
// Allow, a *problem.Problem (403, or 500 on a policy evaluation error)
// otherwise — map it onward with grpcx.Status for a gRPC handler. Most
// call sites want this; use Check directly only if you need the
// Diagnostic for something other than logging.
func (a *Authorizer) Authorize(
	ctx context.Context,
	p *authn.Principal,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) error {
	return a.authorizeEntity(ctx, userActor(p), action, resource, resourceAttrs)
}

// CheckService is Check's mTLS counterpart: it evaluates whether the
// calling *service* (identified by its verified client certificate, via
// pkg/grpcx — see authn.ServiceIdentity) may perform action on resource.
// Use this for calls with no forwarded user token to authorize as (a
// cron job, a reconciler, any call a service makes on its own behalf
// rather than a specific user's) — see authn.ServiceIdentity's doc
// comment for when a call might need both this and Check.
func (a *Authorizer) CheckService(
	ctx context.Context,
	s *authn.ServiceIdentity,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) (Decision, cedar.Diagnostic, error) {
	return a.checkEntity(ctx, serviceActor(s), action, resource, resourceAttrs)
}

// AuthorizeService is CheckService plus the same error mapping Authorize
// applies.
func (a *Authorizer) AuthorizeService(
	ctx context.Context,
	s *authn.ServiceIdentity,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) error {
	return a.authorizeEntity(ctx, serviceActor(s), action, resource, resourceAttrs)
}

func (a *Authorizer) checkEntity(
	ctx context.Context,
	who actor,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) (Decision, cedar.Diagnostic, error) {
	entities := buildEntities(a.roles, who, resource, resourceAttrs)

	req := cedar.Request{
		Principal: who.uid,
		Action:    ActionEntityUID(action),
		Resource:  resource.EntityUID(),
		Context:   cedar.NewRecord(cedar.RecordMap{}),
	}

	decision, diag := cedar.Authorize(a.store.Set(), entities, req)
	if len(diag.Errors) > 0 {
		a.log.ErrorContext(ctx, "authz: policy evaluation error",
			"action", action, "resource_type", resource.Type, "resource_id", resource.ID,
			"errors", diag.Errors)
	}
	return Decision(decision), diag, nil
}

func (a *Authorizer) authorizeEntity(
	ctx context.Context,
	who actor,
	action Action,
	resource ResourceRef,
	resourceAttrs cedar.RecordMap,
) error {
	decision, diag, err := a.checkEntity(ctx, who, action, resource, resourceAttrs)
	if err != nil {
		return problem.Internal(err, a.Debug)
	}
	if decision == Allow {
		return nil
	}

	a.log.WarnContext(ctx, "authz: denied",
		"actor", who.logValue, "action", action,
		"resource_type", resource.Type, "resource_id", resource.ID,
		"roles", who.roles, "reasons", diag.Reasons)

	detail := ""
	if a.Debug {
		detail = "Denied: no permit policy matched (and/or a forbid policy did)."
	}
	return problem.Forbidden(detail)
}
