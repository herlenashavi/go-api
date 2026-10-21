package authn_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/herlenashavi/go-api/pkg/authn"
)

const (
	testIssuer   = "https://idp.example.com/"
	testAudience = "heroes-api"
	testKID      = "test-key"
)

// jwksServer serves a one-key JWKS for key, so NewJWTVerifier exercises
// the real keyfunc fetch path rather than a stub.
func jwksServer(t *testing.T, key *rsa.PrivateKey) *httptest.Server {
	t.Helper()
	pub := key.PublicKey
	body, err := json.Marshal(map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"kid": testKID,
			"alg": "RS256",
			"use": "sig",
			"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
	if err != nil {
		t.Fatalf("marshal JWKS: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKID
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub": "alice",
		"iss": testIssuer,
		"aud": testAudience,
		"exp": time.Now().Add(time.Minute).Unix(),
	}
}

func newTestJWTVerifier(t *testing.T) (authn.Verifier, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, key)
	v, err := authn.NewJWTVerifier(t.Context(), authn.JWTVerifierConfig{
		JWKSURL:  srv.URL,
		Issuer:   testIssuer,
		Audience: testAudience,
	})
	if err != nil {
		t.Fatalf("NewJWTVerifier: %v", err)
	}
	return v, key
}

func TestNewJWTVerifier_RequiresIssuerAndAudience(t *testing.T) {
	cases := map[string]struct {
		cfg     authn.JWTVerifierConfig
		wantErr string
	}{
		"no JWKS URL": {authn.JWTVerifierConfig{Issuer: testIssuer, Audience: testAudience}, "JWKSURL"},
		"no issuer":   {authn.JWTVerifierConfig{JWKSURL: "http://unused", Audience: testAudience}, "Issuer"},
		"no audience": {authn.JWTVerifierConfig{JWKSURL: "http://unused", Issuer: testIssuer}, "Audience"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := authn.NewJWTVerifier(t.Context(), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestNewJWTVerifier_SkipAudienceCheckIsExplicitOptOut(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	srv := jwksServer(t, key)
	v, err := authn.NewJWTVerifier(t.Context(), authn.JWTVerifierConfig{
		JWKSURL: srv.URL, Issuer: testIssuer, SkipAudienceCheck: true,
	})
	if err != nil {
		t.Fatalf("NewJWTVerifier with SkipAudienceCheck: %v", err)
	}
	claims := validClaims()
	claims["aud"] = "some-other-app"
	if _, err := v.Verify(t.Context(), signRS256(t, key, claims)); err != nil {
		t.Fatalf("audience should be unchecked when opted out: %v", err)
	}
}

func TestJWTVerifier_ValidToken(t *testing.T) {
	v, key := newTestJWTVerifier(t)
	p, err := v.Verify(t.Context(), signRS256(t, key, validClaims()))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Subject != "alice" {
		t.Fatalf("subject = %q, want alice", p.Subject)
	}
}

func TestJWTVerifier_RejectsBadClaims(t *testing.T) {
	v, key := newTestJWTVerifier(t)
	cases := map[string]func(jwt.MapClaims){
		"wrong issuer":   func(c jwt.MapClaims) { c["iss"] = "https://evil.example.com/" },
		"no issuer":      func(c jwt.MapClaims) { delete(c, "iss") },
		"wrong audience": func(c jwt.MapClaims) { c["aud"] = "some-other-app" },
		"no audience":    func(c jwt.MapClaims) { delete(c, "aud") },
		"no expiry":      func(c jwt.MapClaims) { delete(c, "exp") },
		"expired":        func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"no subject":     func(c jwt.MapClaims) { delete(c, "sub") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			claims := validClaims()
			mutate(claims)
			if _, err := v.Verify(t.Context(), signRS256(t, key, claims)); err == nil {
				t.Fatal("expected token to be rejected")
			}
		})
	}
}

func TestHMACVerifier_RejectsTokenWithoutExpiry(t *testing.T) {
	secret := []byte("super-secret")
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "alice"}).SignedString(secret)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := authn.NewHMACVerifier(secret, authn.ClaimsConfig{}).Verify(t.Context(), tok); err == nil {
		t.Fatal("expected a token with no exp claim to be rejected")
	}
}

func TestHMACVerifier_RejectsTokenWithoutSubject(t *testing.T) {
	secret := []byte("super-secret")
	tok, err := authn.SignHMAC(secret, authn.Principal{}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}
	if _, err := authn.NewHMACVerifier(secret, authn.ClaimsConfig{}).Verify(t.Context(), tok); err == nil {
		t.Fatal("expected a token with no sub claim to be rejected")
	}
}
