package crud_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestResource_AuthzChecksTheSpecificInstance proves per-instance routes
// authorize against Widget::"<id>", not the collection Widget::"*": a
// policy permitting exactly one ID must allow that one and deny another,
// for every instance-level operation.
func TestResource_AuthzChecksTheSpecificInstance(t *testing.T) {
	store := newMemStore()
	store.items["w1"] = widget{ID: "w1", Name: "A"}
	store.items["w2"] = widget{ID: "w2", Name: "B"}
	a := newTestAuthorizer(t, `
permit (
  principal in Role::"viewer",
  action in [Action::"widget:read", Action::"widget:update", Action::"widget:delete"],
  resource == Widget::"w1"
);
`)
	r := newRouter(t, store, a)

	cases := []struct {
		method, body string
	}{
		{http.MethodGet, ""},
		{http.MethodPatch, `{"name":"Z"}`},
		{http.MethodDelete, ""},
	}
	for _, tc := range cases {
		// Denied for the instance the policy doesn't name.
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, withPrincipal(httptest.NewRequest(tc.method, "/widgets/w2", strings.NewReader(tc.body)), "viewer"))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s /widgets/w2: status = %d, want 403, body=%s", tc.method, rec.Code, rec.Body.String())
		}
	}
	for _, tc := range cases {
		// Allowed for the one it does (DELETE last, since it removes w1).
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, withPrincipal(httptest.NewRequest(tc.method, "/widgets/w1", strings.NewReader(tc.body)), "viewer"))
		if rec.Code >= 400 {
			t.Fatalf("%s /widgets/w1: status = %d, want success, body=%s", tc.method, rec.Code, rec.Body.String())
		}
	}
}
