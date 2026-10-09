package authn

import (
	"context"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// NewHMACVerifier builds a Verifier backed by a single shared secret
// (HS256), for local development and tests only. It is deliberately not the
// production path — the right way to tell developers apart is a real IdP
// via NewJWTVerifier against its JWKS endpoint. Reach for this only in:
//
//   - the example service, so `make run` works with zero external IdP;
//   - integration tests that need to mint tokens for arbitrary principals.
//
// Never configure a deployed service with this verifier: a shared secret
// checked into env vars does not provide the identity guarantees Cedar
// policies are written to assume.
func NewHMACVerifier(secret []byte, claims ClaimsConfig) Verifier {
	return &hmacVerifier{secret: secret, claims: claims.withDefaults()}
}

type hmacVerifier struct {
	secret []byte
	claims ClaimsConfig
}

func (v *hmacVerifier) Verify(_ context.Context, tokenString string) (*Principal, error) {
	keyFunc := func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("authn: unexpected signing method %v", t.Header["alg"])
		}
		return v.secret, nil
	}
	return parseAndBuildPrincipal(tokenString, keyFunc, "", "", []string{"HS256"}, v.claims)
}

// SignHMAC mints a token for principal p, signed with secret using HS256.
// It exists to make NewHMACVerifier usable in examples and tests without a
// hand-rolled JWT library call at every call site; it is not meant to model
// how a real IdP issues tokens.
func SignHMAC(secret []byte, p Principal, ttl time.Duration) (string, error) {
	claims := jwt.MapClaims{
		"sub": p.Subject,
		"exp": time.Now().Add(ttl).Unix(),
		"iat": time.Now().Unix(),
	}
	if p.TenantID != "" {
		claims["tenant_id"] = p.TenantID
	}
	if len(p.Roles) > 0 {
		roles := make([]interface{}, len(p.Roles))
		for i, r := range p.Roles {
			roles[i] = r
		}
		claims["roles"] = roles
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}
