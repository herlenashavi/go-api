package authn

import (
	"context"
	"fmt"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

// Verifier authenticates a bearer token and returns the Principal it
// identifies. Implementations must reject expired, malformed, or
// signature-invalid tokens; Middleware treats any non-nil error as 401.
type Verifier interface {
	Verify(ctx context.Context, token string) (*Principal, error)
}

// ClaimsConfig names the JWT claims go-api reads into a Principal. All have
// sane OIDC-ish defaults; override only the ones your IdP names differently.
type ClaimsConfig struct {
	// RolesClaim holds the caller's roles, as either a JSON array of
	// strings or a single space-delimited string (OAuth2 "scope" style).
	// Defaults to "roles".
	RolesClaim string
	// TenantClaim holds the tenant identifier. Defaults to "tenant_id".
	// Leave the claim absent for single-tenant services.
	TenantClaim string
}

func (c ClaimsConfig) withDefaults() ClaimsConfig {
	if c.RolesClaim == "" {
		c.RolesClaim = "roles"
	}
	if c.TenantClaim == "" {
		c.TenantClaim = "tenant_id"
	}
	return c
}

// JWTVerifierConfig configures production JWKS-based verification.
type JWTVerifierConfig struct {
	// JWKSURL is fetched and cached by keyfunc, which also handles rotation
	// (refetching on an unrecognized "kid").
	JWKSURL string
	// Issuer and Audience are validated against the token's "iss"/"aud".
	// Leave Audience empty to skip audience validation.
	Issuer   string
	Audience string
	// ValidMethods restricts accepted signing algorithms. Defaults to
	// RS256/ES256/PS256 — never include "none" or HMAC methods here for a
	// JWKS-based verifier.
	ValidMethods []string
	Claims       ClaimsConfig
}

type jwtVerifier struct {
	kf           keyfunc.Keyfunc
	issuer       string
	audience     string
	validMethods []string
	claims       ClaimsConfig
	keyFunc      jwt.Keyfunc
}

// NewJWTVerifier builds a Verifier backed by a live JWKS endpoint. It fetches
// the key set immediately (failing fast if the IdP is unreachable at
// startup) and keeps it refreshed in the background for the life of ctx.
func NewJWTVerifier(ctx context.Context, cfg JWTVerifierConfig) (Verifier, error) {
	if cfg.JWKSURL == "" {
		return nil, fmt.Errorf("authn: JWKSURL is required")
	}
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{cfg.JWKSURL})
	if err != nil {
		return nil, fmt.Errorf("authn: fetch JWKS: %w", err)
	}

	methods := cfg.ValidMethods
	if len(methods) == 0 {
		methods = []string{"RS256", "ES256", "PS256"}
	}

	return &jwtVerifier{
		kf:           kf,
		issuer:       cfg.Issuer,
		audience:     cfg.Audience,
		validMethods: methods,
		claims:       cfg.Claims.withDefaults(),
	}, nil
}

func (v *jwtVerifier) Verify(ctx context.Context, tokenString string) (*Principal, error) {
	return parseAndBuildPrincipal(tokenString, v.kf.KeyfuncCtx(ctx), v.issuer, v.audience, v.validMethods, v.claims)
}

func parseAndBuildPrincipal(
	tokenString string,
	keyFunc jwt.Keyfunc,
	issuer, audience string,
	validMethods []string,
	claimsCfg ClaimsConfig,
) (*Principal, error) {
	claims := jwt.MapClaims{}
	opts := []jwt.ParserOption{jwt.WithValidMethods(validMethods)}
	if issuer != "" {
		opts = append(opts, jwt.WithIssuer(issuer))
	}
	if audience != "" {
		opts = append(opts, jwt.WithAudience(audience))
	}

	token, err := jwt.ParseWithClaims(tokenString, claims, keyFunc, opts...)
	if err != nil {
		return nil, fmt.Errorf("authn: invalid token: %w", err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("authn: invalid token")
	}

	sub, _ := claims.GetSubject()
	if sub == "" {
		return nil, fmt.Errorf("authn: token has no subject")
	}

	p := &Principal{
		Subject:  sub,
		TenantID: stringClaim(claims, claimsCfg.TenantClaim),
		Roles:    rolesClaim(claims, claimsCfg.RolesClaim),
		Claims:   claims,
	}
	return p, nil
}

func stringClaim(claims jwt.MapClaims, key string) string {
	if key == "" {
		return ""
	}
	if v, ok := claims[key].(string); ok {
		return v
	}
	return ""
}

// rolesClaim accepts either a JSON array of strings or a single
// space-delimited string (the OAuth2 "scope" convention), since IdPs are
// inconsistent about which they use for role-like claims.
func rolesClaim(claims jwt.MapClaims, key string) []string {
	if key == "" {
		return nil
	}
	switch v := claims[key].(type) {
	case []interface{}:
		roles := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				roles = append(roles, s)
			}
		}
		return roles
	case string:
		if v == "" {
			return nil
		}
		return splitSpace(v)
	default:
		return nil
	}
}

func splitSpace(s string) []string {
	var out []string
	start := -1
	for i, r := range s {
		if r == ' ' {
			if start >= 0 {
				out = append(out, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	if start >= 0 {
		out = append(out, s[start:])
	}
	return out
}
