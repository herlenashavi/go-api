package authn

import (
	"net/http"
	"strings"

	"github.com/herlenashavi/go-api/pkg/problem"
)

// Middleware authenticates every request through it using verifier. On
// success the resulting Principal is attached to the request context
// (retrievable via FromContext); on failure it writes a 401 problem+json
// response and does not call the next handler.
//
// Mount it only on routes that require authentication — chi's r.Group /
// r.With let you apply it to a subset of routes (e.g. everything except
// /health/*):
//
//	r.With(authn.Middleware(verifier)).Mount("/v1", v1Router)
func Middleware(verifier Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := bearerToken(r)
			if err != nil {
				problem.Write(w, r, problem.Unauthorized(err.Error()))
				return
			}

			principal, err := verifier.Verify(r.Context(), token)
			if err != nil {
				problem.Write(w, r, problem.Unauthorized("The provided credentials are invalid or expired."))
				return
			}

			ctx := WithPrincipal(r.Context(), principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", errMissingAuth
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", errMalformedAuth
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" {
		return "", errMalformedAuth
	}
	return token, nil
}

var (
	errMissingAuth   = authError("A bearer token is required in the Authorization header.")
	errMalformedAuth = authError(`The Authorization header must be in the form "Bearer <token>".`)
)

type authError string

func (e authError) Error() string { return string(e) }
