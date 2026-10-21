package authz_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
)

// TestMiddleware_IDParamTargetsTheSpecificInstance proves Route.IDParam
// makes Middleware authorize against Hero::"<id>" rather than the
// collection Hero::"*": a policy naming one ID allows that one and denies
// any other.
func TestMiddleware_IDParamTargetsTheSpecificInstance(t *testing.T) {
	dir := t.TempDir()
	policy := `permit (principal in Role::"viewer", action == Action::"heroes:read", resource == Hero::"h1");`
	if err := os.WriteFile(filepath.Join(dir, "p.cedar"), []byte(policy), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	store, err := authz.LoadPolicyDir(dir)
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	roles, err := authz.NewRoleRegistry(authz.Role{Name: "viewer"})
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}
	a := authz.New(store, roles, nil)

	r := chi.NewRouter()
	r.With(authz.Middleware(a, authz.Route{
		Action: "heroes:read", ResourceType: "Hero", IDParam: "id",
	})).Get("/heroes/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	for id, want := range map[string]int{"h1": http.StatusOK, "h2": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/heroes/"+id, nil)
		req = req.WithContext(authn.WithPrincipal(req.Context(), &authn.Principal{Subject: "alice", Roles: []string{"viewer"}}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("GET /heroes/%s: status = %d, want %d", id, rec.Code, want)
		}
	}
}

// TestMiddleware_NoPrincipalIs401 covers the guard for a route mounted
// without authn.Middleware in front of it.
func TestMiddleware_NoPrincipalIs401(t *testing.T) {
	a := newTestAuthorizer(t)
	h := authz.Middleware(a, authz.Route{Action: "heroes:read", ResourceType: "Hero"})(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}
