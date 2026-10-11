package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func serveWithSecurityHeaders(t *testing.T, csp string) http.Header {
	t.Helper()
	h := SecurityHeaders(csp)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	return rec.Header()
}

func TestSecurityHeaders_SetsConservativeDefaults(t *testing.T) {
	got := serveWithSecurityHeaders(t, "")

	want := map[string]string{
		"X-Content-Type-Options":     "nosniff",
		"X-Frame-Options":            "DENY",
		"Referrer-Policy":            "strict-origin-when-cross-origin",
		"Cross-Origin-Opener-Policy": "same-origin",
		"Strict-Transport-Security":  "max-age=31536000; includeSubDomains",
	}
	for header, value := range want {
		if got.Get(header) != value {
			t.Errorf("%s = %q, want %q", header, got.Get(header), value)
		}
	}
}

// TestSecurityHeaders_CSPOmittedWhenEmpty pins the documented default: a
// CSP is specific to whatever docs UI a service serves, so the framework
// sets none rather than guessing one that would break an embedded UI.
func TestSecurityHeaders_CSPOmittedWhenEmpty(t *testing.T) {
	if got := serveWithSecurityHeaders(t, ""); got.Get("Content-Security-Policy") != "" {
		t.Fatalf("Content-Security-Policy = %q, want it unset", got.Get("Content-Security-Policy"))
	}
}

func TestSecurityHeaders_CSPSetWhenConfigured(t *testing.T) {
	const csp = "default-src 'self'"
	if got := serveWithSecurityHeaders(t, csp).Get("Content-Security-Policy"); got != csp {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, csp)
	}
}

// TestSecurityHeaders_AppliedBeforeHandlerWrites is the ordering
// property that makes the middleware work at all: headers must be set
// before the handler can commit the response, or WriteHeader has already
// flushed them and the additions are silently dropped.
func TestSecurityHeaders_AppliedBeforeHandlerWrites(t *testing.T) {
	h := SecurityHeaders("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("body"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security headers were lost once the handler committed the response")
	}
}
