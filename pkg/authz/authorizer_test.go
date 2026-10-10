package authz_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/herlenashavi/go-api/pkg/authn"
	"github.com/herlenashavi/go-api/pkg/authz"
)

const testPolicies = `
permit (
  principal in Role::"viewer",
  action == Action::"heroes:read",
  resource
);

permit (
  principal in Role::"editor",
  action == Action::"heroes:update",
  resource
);

permit (
  principal in Role::"admin",
  action == Action::"heroes:delete",
  resource
);
`

func newTestAuthorizer(t *testing.T) *authz.Authorizer {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "heroes.cedar"), []byte(testPolicies), 0o644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	store, err := authz.LoadPolicyDir(dir)
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}

	// admin > editor > viewer: a role's Parents are what it inherits from.
	roles, err := authz.NewRoleRegistry(
		authz.Role{Name: "viewer"},
		authz.Role{Name: "editor", Parents: []string{"viewer"}},
		authz.Role{Name: "admin", Parents: []string{"editor"}},
	)
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}

	return authz.New(store, roles, nil)
}

func TestAuthorizer_RoleHierarchy(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()
	hero := authz.ResourceRef{Type: "Hero", ID: "h1"}

	cases := []struct {
		name   string
		roles  []string
		action authz.Action
		want   authz.Decision
	}{
		{"viewer can read", []string{"viewer"}, "heroes:read", authz.Allow},
		{"viewer cannot update", []string{"viewer"}, "heroes:update", authz.Deny},
		{"editor inherits viewer's read", []string{"editor"}, "heroes:read", authz.Allow},
		{"editor can update", []string{"editor"}, "heroes:update", authz.Allow},
		{"editor cannot delete", []string{"editor"}, "heroes:delete", authz.Deny},
		{"admin can delete", []string{"admin"}, "heroes:delete", authz.Allow},
		{"admin inherits editor's update", []string{"admin"}, "heroes:update", authz.Allow},
		{"no roles denied by default", nil, "heroes:read", authz.Deny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &authn.Principal{Subject: "alice", Roles: tc.roles}
			decision, diag, err := a.Check(ctx, p, tc.action, hero, nil)
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if decision != tc.want {
				t.Fatalf("got %v, want %v (diagnostic reasons: %+v)", decision, tc.want, diag.Reasons)
			}
		})
	}
}

func TestAuthorizer_Authorize_MapsToProblem(t *testing.T) {
	a := newTestAuthorizer(t)
	ctx := context.Background()
	hero := authz.ResourceRef{Type: "Hero", ID: "h1"}

	p := &authn.Principal{Subject: "bob", Roles: []string{"viewer"}}
	if err := a.Authorize(ctx, p, "heroes:read", hero, nil); err != nil {
		t.Fatalf("expected read to be authorized, got %v", err)
	}

	err := a.Authorize(ctx, p, "heroes:delete", hero, nil)
	if err == nil {
		t.Fatal("expected delete to be denied")
	}
}

func TestPolicyStore_Reload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "heroes.cedar")
	if err := os.WriteFile(path, []byte(testPolicies), 0o644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}

	store, err := authz.LoadPolicyDir(dir)
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	roles, _ := authz.NewRoleRegistry(authz.Role{Name: "viewer"})
	a := authz.New(store, roles, nil)

	ctx := context.Background()
	hero := authz.ResourceRef{Type: "Hero", ID: "h1"}
	p := &authn.Principal{Subject: "alice", Roles: []string{"viewer"}}

	if decision, _, _ := a.Check(ctx, p, "heroes:read", hero, nil); decision != authz.Allow {
		t.Fatal("expected read to be allowed before reload")
	}

	// Replace the policy file with one that denies everything, then reload.
	if err := os.WriteFile(path, []byte(`forbid (principal, action, resource);`), 0o644); err != nil {
		t.Fatalf("rewrite policy file: %v", err)
	}
	if err := store.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if decision, _, _ := a.Check(ctx, p, "heroes:read", hero, nil); decision != authz.Deny {
		t.Fatal("expected read to be denied after reload")
	}
}

const servicePolicies = `
// an exact-name grant -- no role needed, matches CheckService's doc
// comment on referencing a specific service by identity.
permit (
  principal == Service::"heroes",
  action == Action::"villain:read",
  resource
);

// a role-based grant -- the same RoleRegistry hierarchy a User's roles
// resolve through, just granted via TLSConfig.ServiceRoles instead of a
// JWT claim.
permit (
  principal in Role::"trusted-caller",
  action == Action::"villain:list",
  resource
);
`

func newServiceTestAuthorizer(t *testing.T) *authz.Authorizer {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "villains.cedar"), []byte(servicePolicies), 0o644); err != nil {
		t.Fatalf("write policy file: %v", err)
	}
	store, err := authz.LoadPolicyDir(dir)
	if err != nil {
		t.Fatalf("LoadPolicyDir: %v", err)
	}
	roles, err := authz.NewRoleRegistry(authz.Role{Name: "trusted-caller"})
	if err != nil {
		t.Fatalf("NewRoleRegistry: %v", err)
	}
	return authz.New(store, roles, nil)
}

// TestAuthorizer_CheckService proves Cedar policies can be written
// against a calling *service*'s identity (authn.ServiceIdentity, as
// placed in context by pkg/grpcx's mTLS peer-identity interceptor) —
// either by exact name or, like a user's roles, via role membership —
// using the same Authorizer and RoleRegistry a User-principal check
// would.
func TestAuthorizer_CheckService(t *testing.T) {
	a := newServiceTestAuthorizer(t)
	ctx := context.Background()
	villain := authz.ResourceRef{Type: "Villain", ID: "v1"}

	cases := []struct {
		name   string
		svc    *authn.ServiceIdentity
		action authz.Action
		want   authz.Decision
	}{
		{"heroes can read by exact identity", &authn.ServiceIdentity{Name: "heroes"}, "villain:read", authz.Allow},
		{"villains itself cannot read (no grant for its own name)", &authn.ServiceIdentity{Name: "villains"}, "villain:read", authz.Deny},
		{"trusted-caller role can list", &authn.ServiceIdentity{Name: "some-batch-job", Roles: []string{"trusted-caller"}}, "villain:list", authz.Allow},
		{"heroes has no role, cannot list", &authn.ServiceIdentity{Name: "heroes"}, "villain:list", authz.Deny},
		{"unknown service denied by default", &authn.ServiceIdentity{Name: "unknown"}, "villain:read", authz.Deny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, diag, err := a.CheckService(ctx, tc.svc, tc.action, villain, nil)
			if err != nil {
				t.Fatalf("CheckService: %v", err)
			}
			if decision != tc.want {
				t.Fatalf("got %v, want %v (diagnostic reasons: %+v)", decision, tc.want, diag.Reasons)
			}
		})
	}
}

func TestAuthorizer_AuthorizeService_MapsToProblem(t *testing.T) {
	a := newServiceTestAuthorizer(t)
	ctx := context.Background()
	villain := authz.ResourceRef{Type: "Villain", ID: "v1"}

	heroes := &authn.ServiceIdentity{Name: "heroes"}
	if err := a.AuthorizeService(ctx, heroes, "villain:read", villain, nil); err != nil {
		t.Fatalf("expected heroes to be authorized to read, got %v", err)
	}

	if err := a.AuthorizeService(ctx, heroes, "villain:list", villain, nil); err == nil {
		t.Fatal("expected heroes (no trusted-caller role) to be denied list")
	}
}
