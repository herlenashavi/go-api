// Package authn establishes *who* is calling — authentication only.
// Authorization (*what* they're allowed to do) is a separate concern,
// handled by pkg/authz's Cedar engine against the Principal this package
// puts in context. Keeping the two apart means swapping how you verify
// identity (JWT today, mTLS tomorrow) never touches a single policy.
package authn

import (
	"context"
	"log/slog"
)

// Principal is the authenticated caller. It is deliberately minimal and
// transport-agnostic: nothing here assumes JWTs specifically, so a non-JWT
// Verifier (mTLS, an internal service token, API keys) can populate the same
// struct and every downstream package — authz, logging, crud — keeps working.
type Principal struct {
	// Subject is the stable, unique identifier for the caller (JWT "sub").
	Subject string
	// TenantID scopes the caller to a tenant in a multi-tenant deployment.
	// Empty if the service is single-tenant.
	TenantID string
	// Roles are coarse-grained grants (e.g. "admin", "billing-viewer").
	// go-api's authz package maps each Role to a Cedar entity and uses
	// Cedar's entity hierarchy (parents) to resolve role -> permissions, so
	// "what a role can do" lives in policy files, not in this struct.
	Roles []string
	// Claims holds the raw, verified claim set for anything role-based
	// authz doesn't cover (e.g. passing a claim through as Cedar context).
	Claims map[string]any
}

// HasRole reports whether p was granted role, exactly as asserted by the
// Verifier (role hierarchy/inheritance is a Cedar policy concern, not
// resolved here).
func (p *Principal) HasRole(role string) bool {
	if p == nil {
		return false
	}
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type principalKey struct{}
type rawTokenKey struct{}

// WithPrincipal returns a context carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the Principal set on ctx by the authn middleware, and
// whether one was present. Handlers behind Middleware can assume ok == true;
// handlers on an unauthenticated route should check it.
func FromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok
}

// WithRawToken returns a context carrying the verified bearer token
// verbatim, alongside the Principal it was verified into. This exists for
// interservice calls: a handler that calls another service on the current
// caller's behalf can forward the same token (via pkg/grpcx's client
// interceptor) so the downstream service authenticates — and authorizes,
// if it also uses pkg/authz — as the same principal, rather than needing
// its own service-to-service credential.
//
// Forwarding a user's token to a downstream service is a real trust
// decision — only do it for services you'd trust with that user's
// identity anyway, the same as you'd think about forwarding any bearer
// token. Prefer a distinct service credential for anything that isn't
// strictly "acting on this specific user's behalf."
func WithRawToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, rawTokenKey{}, token)
}

// RawTokenFromContext returns the token stored by WithRawToken, if any.
func RawTokenFromContext(ctx context.Context) (string, bool) {
	t, ok := ctx.Value(rawTokenKey{}).(string)
	return t, ok
}

// LogFields is a logging.Fielder: it attaches the caller's subject and
// tenant to every log line emitted while handling an authenticated request.
// Roles are intentionally omitted from logs by default to avoid noisy,
// high-cardinality fields; add them yourself via a custom Fielder if needed.
func LogFields(ctx context.Context) []slog.Attr {
	p, ok := FromContext(ctx)
	if !ok || p == nil {
		return nil
	}
	attrs := make([]slog.Attr, 0, 2)
	if p.Subject != "" {
		attrs = append(attrs, slog.String("actor", p.Subject))
	}
	if p.TenantID != "" {
		attrs = append(attrs, slog.String("tenant", p.TenantID))
	}
	return attrs
}
