package authn

import (
	"context"
	"crypto/x509"
	"log/slog"
)

// ServiceIdentity is the mTLS counterpart to Principal: it identifies
// *which service* is calling, verified by the TLS handshake itself (the
// caller presented a client certificate signed by a CA this service
// trusts), rather than a bearer token asserting a human user. It is set
// by pkg/grpcx's server when Dependencies.MTLS is configured — see that
// package's doc and docs/grpc.md's mTLS section.
//
// A single RPC can carry a ServiceIdentity, a Principal (a forwarded
// user token), both, or neither:
//
//   - ServiceIdentity only: a service calling on its own behalf (a cron
//     job, a reconciler) — there is no user to act on behalf of.
//   - Principal only: mTLS isn't configured; identity is bearer-token-only,
//     same as the HTTP side.
//   - Both: the recommended production shape for a user-initiated
//     interservice call — mTLS proves *which service* is calling, the
//     forwarded token proves *which user* it's calling on behalf of.
//     Authorize against whichever (or both) your policy needs.
type ServiceIdentity struct {
	// Name identifies the calling service. By convention this is the
	// client certificate's first URI SAN if it looks like a SPIFFE ID
	// (spiffe://trust-domain/ns/.../sa/name — common with cert-manager,
	// Istio, SPIRE), falling back to the certificate's Subject Common
	// Name otherwise. See pkg/grpcx's cert-loading code for exactly how
	// this is derived.
	Name string
	// Roles are coarse-grained grants, exactly like Principal.Roles —
	// assigned by whatever issues service certificates in your
	// deployment (e.g. a Cedar RoleRegistry entry per trust tier), not
	// derived from the certificate itself.
	Roles []string
	// Cert is the verified leaf certificate presented by the caller, for
	// anything beyond Name/Roles a handler might need (e.g. its
	// NotAfter, for a service that wants to warn on soon-to-expire
	// peers).
	Cert *x509.Certificate
}

// HasRole mirrors Principal.HasRole.
func (s *ServiceIdentity) HasRole(role string) bool {
	if s == nil {
		return false
	}
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}

type serviceIdentityKey struct{}

// WithServiceIdentity returns a context carrying s.
func WithServiceIdentity(ctx context.Context, s *ServiceIdentity) context.Context {
	return context.WithValue(ctx, serviceIdentityKey{}, s)
}

// ServiceIdentityFromContext returns the ServiceIdentity set on ctx by
// pkg/grpcx's mTLS peer-identity interceptor, and whether one was
// present. Absent (ok == false) on any call that didn't present a client
// certificate — which, unless Dependencies.MTLS.RequireClientCert is set
// server-side, includes perfectly ordinary unauthenticated-by-mTLS calls,
// not just attacks; check ok before treating its absence as suspicious.
func ServiceIdentityFromContext(ctx context.Context) (*ServiceIdentity, bool) {
	s, ok := ctx.Value(serviceIdentityKey{}).(*ServiceIdentity)
	return s, ok
}

// ServiceLogFields is a logging.Fielder: it attaches the calling
// service's name to every log line emitted while handling a
// mTLS-authenticated request. Pair with LogFields (for the forwarded
// user, if any) — a request can have both.
func ServiceLogFields(ctx context.Context) []slog.Attr {
	s, ok := ServiceIdentityFromContext(ctx)
	if !ok || s == nil || s.Name == "" {
		return nil
	}
	return []slog.Attr{slog.String("calling_service", s.Name)}
}
