package httpx

import (
	"context"
	"net/http"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// NoContent is the Out type for handlers that return no response body
// (DELETE endpoints, typically). Handle writes 204 for it regardless of
// WithStatus.
type NoContent struct{}

type handlerConfig struct {
	status int
}

// Option configures Handle.
type Option func(*handlerConfig)

// WithStatus sets the HTTP status written on success (default 200).
// Ignored when Out is NoContent, which always writes 204.
func WithStatus(status int) Option {
	return func(c *handlerConfig) { c.status = status }
}

// Handle adapts a typed handler function into an http.HandlerFunc:
//
//  1. Decode[In](r) — path/query/header tags, then JSON body.
//  2. Validate(in) — struct tag validation, if In has any `validate` tags.
//  3. fn(ctx, in) — your handler. Return a *problem.Problem (or any error;
//     non-Problem errors become a generic 500 via problem.From) to fail the
//     request; httpx writes it as application/problem+json.
//  4. Encode the success result as JSON with the configured status.
//
// debug controls whether unexpected (non-Problem) errors leak their message
// to the client — pass your service's config.Base.Debug.
func Handle[In, Out any](fn func(ctx context.Context, in *In) (*Out, error), debug bool, opts ...Option) http.HandlerFunc {
	cfg := handlerConfig{status: http.StatusOK}
	for _, opt := range opts {
		opt(&cfg)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		in, err := Decode[In](r)
		if err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		if err := Validate(in); err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		out, err := fn(r.Context(), in)
		if err != nil {
			problem.Write(w, r, problem.From(err, debug))
			return
		}

		if _, isNoContent := any(out).(*NoContent); isNoContent {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		Respond(w, r, cfg.status, out)
	}
}
