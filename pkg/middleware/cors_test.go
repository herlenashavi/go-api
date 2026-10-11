package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// corsRequest sends a simple cross-origin GET from origin through a CORS
// middleware configured with allowed, and returns the response.
func corsRequest(t *testing.T, allowed []string, origin string) *httptest.ResponseRecorder {
	t.Helper()
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/heroes", nil)
	req.Header.Set("Origin", origin)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// corsPreflight sends an OPTIONS preflight asking to use method and
// headers. Per the CORS spec (and go-chi/cors' implementation of it) the
// response echoes back the requested method and headers if they're
// permitted, rather than enumerating the full allowlist — so what a
// preflight assertion checks is "was this request approved", not "what is
// configured".
func corsPreflight(t *testing.T, allowed []string, origin, method string, headers []string) *httptest.ResponseRecorder {
	t.Helper()
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("a preflight request should be answered by the CORS middleware, not the handler")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/v1/heroes", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", method)
	if len(headers) > 0 {
		req.Header.Set("Access-Control-Request-Headers", strings.Join(headers, ", "))
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestCORS_NoOriginsAllowsNothing pins the deliberate default documented
// on CORS: an unset CORS_ALLOWED_ORIGINS must not behave like "*".
func TestCORS_NoOriginsAllowsNothing(t *testing.T) {
	rec := corsRequest(t, nil, "https://evil.example")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want it unset with no configured origins", got)
	}
}

func TestCORS_AllowsConfiguredOrigin(t *testing.T) {
	const origin = "https://app.example.com"
	rec := corsRequest(t, []string{origin}, origin)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("Access-Control-Allow-Origin = %q, want %q", got, origin)
	}
}

func TestCORS_RejectsUnlistedOrigin(t *testing.T) {
	rec := corsRequest(t, []string{"https://app.example.com"}, "https://evil.example")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "https://evil.example" {
		t.Fatal("an unlisted origin was reflected back as allowed")
	}
}

// TestCORS_CredentialsOnlyWithExplicitOrigins covers the invalid-combination
// note in CORS's doc comment: "*" plus credentials is rejected by browsers
// outright, so credentials are enabled only when real origins are listed.
func TestCORS_CredentialsOnlyWithExplicitOrigins(t *testing.T) {
	const origin = "https://app.example.com"

	withOrigins := corsRequest(t, []string{origin}, origin)
	if withOrigins.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Error("credentials should be allowed when explicit origins are configured")
	}

	withNone := corsRequest(t, nil, origin)
	if withNone.Header().Get("Access-Control-Allow-Credentials") == "true" {
		t.Error("credentials should not be allowed when no origins are configured")
	}
}

// TestCORS_PreflightApprovesConfiguredMethodsAndHeaders walks every
// method and header the middleware is configured to allow, asserting each
// is approved when a browser actually asks for it.
func TestCORS_PreflightApprovesConfiguredMethodsAndHeaders(t *testing.T) {
	const origin = "https://app.example.com"
	headers := []string{"Accept", "Authorization", "Content-Type", RequestIDHeader}

	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		t.Run(method, func(t *testing.T) {
			rec := corsPreflight(t, []string{origin}, origin, method, headers)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
				t.Fatalf("Access-Control-Allow-Origin = %q, want %q — preflight was not approved", got, origin)
			}
			if got := rec.Header().Get("Access-Control-Allow-Methods"); !containsToken(got, method) {
				t.Errorf("Access-Control-Allow-Methods = %q, want %s echoed back", got, method)
			}
			allowed := rec.Header().Get("Access-Control-Allow-Headers")
			for _, want := range headers {
				if !containsToken(allowed, want) {
					t.Errorf("Access-Control-Allow-Headers = %q, missing %s", allowed, want)
				}
			}
		})
	}
}

// TestCORS_PreflightRejectsUnconfiguredMethodAndHeader is the flip side:
// the allowlist has to actually exclude something, or the assertions above
// would pass against a middleware that approves everything.
func TestCORS_PreflightRejectsUnconfiguredMethodAndHeader(t *testing.T) {
	const origin = "https://app.example.com"

	t.Run("method not in the allowlist", func(t *testing.T) {
		rec := corsPreflight(t, []string{origin}, origin, "TRACE", nil)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want a TRACE preflight rejected", got)
		}
	})

	t.Run("header not in the allowlist", func(t *testing.T) {
		rec := corsPreflight(t, []string{origin}, origin, "GET", []string{"X-Custom-Secret"})
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("Access-Control-Allow-Origin = %q, want an unlisted request header rejected", got)
		}
	})
}

// TestCORS_PreflightRejectedWithNoConfiguredOrigins is the preflight
// counterpart to TestCORS_NoOriginsAllowsNothing — the deny-all default
// has to hold on the OPTIONS path too, not just simple requests.
func TestCORS_PreflightRejectedWithNoConfiguredOrigins(t *testing.T) {
	rec := corsPreflight(t, nil, "https://evil.example", "PUT", []string{"Authorization"})

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want no origin approved by default", got)
	}
}

// TestCORS_ExposesRequestIDHeader matters for support correlation from a
// browser: without this, JavaScript cannot read the X-Request-ID that
// middleware.RequestID echoes, so a user can't report it.
func TestCORS_ExposesRequestIDHeader(t *testing.T) {
	const origin = "https://app.example.com"
	rec := corsRequest(t, []string{origin}, origin)

	if got := rec.Header().Get("Access-Control-Expose-Headers"); !containsToken(got, RequestIDHeader) {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it to include %s", got, RequestIDHeader)
	}
}

// containsToken reports whether a comma-separated header value contains
// token, case-insensitively, without depending on how the CORS library
// spaces or orders its list.
func containsToken(headerValue, token string) bool {
	for _, part := range strings.Split(headerValue, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}
