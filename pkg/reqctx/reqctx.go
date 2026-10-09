// Package reqctx holds the one piece of request-scoped state shared across
// otherwise-independent packages: the request ID. It exists only to avoid an
// import cycle between middleware (which generates the ID), logging (which
// logs it), and problem (which echoes it back to the client).
//
// Everything else request-scoped (the authenticated principal, a database
// transaction) is owned by the package that creates it (authn, dbx) and
// lives in its own context key.
package reqctx

import "context"

type requestIDKey struct{}

// WithRequestID returns a context carrying id, retrievable via RequestID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx, or "" if none is set.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
