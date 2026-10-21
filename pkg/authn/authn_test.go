package authn_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/herlenashavi/go-api/pkg/authn"
)

func TestHMACVerifier_ValidToken(t *testing.T) {
	secret := []byte("super-secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})

	token, err := authn.SignHMAC(secret, authn.Principal{
		Subject: "alice", TenantID: "acme", Roles: []string{"admin", "viewer"},
	}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}

	p, err := verifier.Verify(t.Context(), token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if p.Subject != "alice" || p.TenantID != "acme" {
		t.Fatalf("unexpected principal: %+v", p)
	}
	if !p.HasRole("admin") || !p.HasRole("viewer") || p.HasRole("nope") {
		t.Fatalf("unexpected roles: %v", p.Roles)
	}
}

func TestHMACVerifier_ExpiredToken(t *testing.T) {
	secret := []byte("super-secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})

	token, err := authn.SignHMAC(secret, authn.Principal{Subject: "alice"}, -time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}

	if _, err := verifier.Verify(t.Context(), token); err == nil {
		t.Fatal("expected expired token to fail verification")
	}
}

func TestHMACVerifier_WrongSecretRejected(t *testing.T) {
	token, err := authn.SignHMAC([]byte("secret-a"), authn.Principal{Subject: "alice"}, time.Minute)
	if err != nil {
		t.Fatalf("SignHMAC: %v", err)
	}

	verifier := authn.NewHMACVerifier([]byte("secret-b"), authn.ClaimsConfig{})
	if _, err := verifier.Verify(t.Context(), token); err == nil {
		t.Fatal("expected token signed with a different secret to fail verification")
	}
}

func TestMiddleware_MissingAndMalformedHeader(t *testing.T) {
	verifier := authn.NewHMACVerifier([]byte("secret"), authn.ClaimsConfig{})
	h := authn.Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run without a valid token")
	}))

	for _, authHeader := range []string{"", "Token abc", "Bearer"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization=%q: status = %d, want 401", authHeader, rec.Code)
		}
	}
}

func TestMiddleware_ValidTokenSetsPrincipal(t *testing.T) {
	secret := []byte("secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})
	h := authn.Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := authn.FromContext(r.Context())
		if !ok || p.Subject != "bob" {
			t.Fatalf("expected principal bob in context, got %+v (ok=%v)", p, ok)
		}
		w.WriteHeader(http.StatusOK)
	}))

	token, _ := authn.SignHMAC(secret, authn.Principal{Subject: "bob"}, time.Minute)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// The auth-scheme is case-insensitive per RFC 7235 §2.1.
func TestMiddleware_BearerSchemeIsCaseInsensitive(t *testing.T) {
	secret := []byte("secret")
	verifier := authn.NewHMACVerifier(secret, authn.ClaimsConfig{})
	h := authn.Middleware(verifier)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	token, _ := authn.SignHMAC(secret, authn.Principal{Subject: "bob"}, time.Minute)

	for _, scheme := range []string{"Bearer", "bearer", "BEARER", "bEaReR"} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Authorization", scheme+" "+token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("scheme %q: status = %d, want 200", scheme, rec.Code)
		}
	}
}

func TestUsesPublicDevSecret(t *testing.T) {
	cases := []struct {
		name     string
		verifier authn.Verifier
		want     bool
	}{
		{"default secret", authn.NewHMACVerifier([]byte(authn.DefaultDevSecret), authn.ClaimsConfig{}), true},
		{"empty secret", authn.NewHMACVerifier(nil, authn.ClaimsConfig{}), true},
		{"own secret", authn.NewHMACVerifier([]byte("s3cret-from-a-vault"), authn.ClaimsConfig{}), false},
		{"no verifier", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authn.UsesPublicDevSecret(tc.verifier); got != tc.want {
				t.Fatalf("UsesPublicDevSecret = %v, want %v", got, tc.want)
			}
		})
	}
}
