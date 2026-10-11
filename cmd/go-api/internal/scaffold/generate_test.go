package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGenerateCombinations exercises Generate across the flag combinations
// that matter — every Authn mode, with/without Authz, with/without DB,
// with/without resources. render() already runs go/format.Source over
// every .go file (see templates.go), so a failure here means a template
// produced text that doesn't even parse as Go; this is the regression net
// for template-syntax bugs across combinations nobody tests by hand.
func TestGenerateCombinations(t *testing.T) {
	cases := []struct {
		name  string
		authn AuthnMode
		authz bool
		db    bool
		cruds []string
	}{
		{"minimal", AuthnNone, false, false, nil},
		{"hmac-only", AuthnHMAC, false, false, nil},
		{"jwt-only", AuthnJWT, false, false, nil},
		{"db-no-resources", AuthnHMAC, false, true, nil},
		{"db-one-resource", AuthnHMAC, false, true, []string{"Widget"}},
		{"full", AuthnJWT, true, true, []string{"Order", "LineItem"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			spec, err := ParseSpec(c.name, "github.com/acme/"+c.name, dir, false, string(c.authn), c.authz, c.db, c.cruds, false, false)
			if err != nil {
				t.Fatalf("ParseSpec: %v", err)
			}
			paths, err := Generate(spec)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for _, p := range paths {
				if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
					t.Errorf("planned file %s was not written: %v", p, err)
				}
			}
		})
	}
}

// TestGenerateDBWithoutResourcesSeedsAMigration is a regression test for a
// real bug: migrations/migrations.go's `//go:embed *.sql` fails to compile
// (a Go embed pattern matching zero files is a compile error, not caught
// by go/format.Source) whenever -db is set with no -crud resources, since
// resource migrations were the only source of .sql files. Generate must
// always seed at least one.
func TestGenerateDBWithoutResourcesSeedsAMigration(t *testing.T) {
	dir := t.TempDir()
	spec, err := ParseSpec("svc", "github.com/acme/svc", dir, false, "hmac", false, true, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "migrations"))
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	var sqlFiles int
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			sqlFiles++
		}
	}
	if sqlFiles == 0 {
		t.Fatal("migrations/ has no .sql files; migrations.go's //go:embed *.sql would fail to compile")
	}
}

func TestGenerateRefusesNonEmptyDirWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	spec, err := ParseSpec("svc", "github.com/acme/svc", dir, false, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err == nil {
		t.Fatal("expected Generate to refuse a non-empty directory without -force")
	}

	spec.Force = true
	if _, err := Generate(spec); err != nil {
		t.Fatalf("Generate with Force: %v", err)
	}
}

func TestPlanDryRunMatchesGenerate(t *testing.T) {
	dir := t.TempDir()
	base, err := ParseSpec("svc", "github.com/acme/svc", dir, false, "jwt", true, true, []string{"Order"}, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}

	dry := base
	dry.DryRun = true
	dryPaths, err := Generate(dry)
	if err != nil {
		t.Fatalf("Generate(dry-run): %v", err)
	}

	realPaths, err := Generate(base)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if len(dryPaths) != len(realPaths) {
		t.Fatalf("dry-run listed %d paths, real generation wrote %d", len(dryPaths), len(realPaths))
	}
	for i := range dryPaths {
		if dryPaths[i] != realPaths[i] {
			t.Errorf("path %d: dry-run %q != real %q", i, dryPaths[i], realPaths[i])
		}
	}
}

// writeRootGoMod creates a minimal go.mod at dir — a fake monorepo root
// for the -monorepo tests below, which never need real dependency
// resolution (that happens only when the *generated* service's own
// `go mod tidy` runs, never during generation itself).
func writeRootGoMod(t *testing.T, dir, module string, requireGoAPI bool) {
	t.Helper()
	content := "module " + module + "\n\ngo 1.23\n"
	if requireGoAPI {
		content += "\nrequire " + GoAPIModule + " v0.0.0\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGenerateMonorepo exercises -monorepo end to end against a fake
// repo root: module auto-detected from the enclosing go.mod, internal
// import paths computed from the service's position inside it, and
// go.mod/.gitignore skipped (there's nothing to generate them into).
func TestGenerateMonorepo(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root, "github.com/acme/monorepo", false)
	serviceDir := filepath.Join(root, "services", "orders")

	spec, err := ParseSpec("orders", "", serviceDir, true, "hmac", false, true, []string{"Order"}, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	paths, err := Generate(spec)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	for _, skipped := range []string{"go.mod", ".gitignore"} {
		for _, p := range paths {
			if p == skipped {
				t.Errorf("Generate wrote %s in -monorepo mode; a monorepo has one root %s already", skipped, skipped)
			}
		}
	}

	migrateMain, err := os.ReadFile(filepath.Join(serviceDir, "migrate", "main.go"))
	if err != nil {
		t.Fatalf("read migrate/main.go: %v", err)
	}
	wantImport := `"github.com/acme/monorepo/services/orders/migrations"`
	if !strings.Contains(string(migrateMain), wantImport) {
		t.Errorf("migrate/main.go does not import %s:\n%s", wantImport, migrateMain)
	}

	dockerfile, err := os.ReadFile(filepath.Join(serviceDir, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	wantBuild := "go build -o /out/orders ./services/orders"
	if !strings.Contains(string(dockerfile), wantBuild) {
		t.Errorf("Dockerfile does not contain %q:\n%s", wantBuild, dockerfile)
	}

	readme, err := os.ReadFile(filepath.Join(serviceDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if !strings.Contains(string(readme), "## Monorepo") {
		t.Error("README.md has no Monorepo section")
	}
	if !strings.Contains(string(readme), "first go-api-based service") {
		t.Error("README.md should note go-api isn't yet a dependency of the detected root module")
	}
}

// TestGenerateMonorepoCwdIsRootPrintsNoOpCd is a regression test: when
// the current working directory *is* the detected root (the common
// case — running `go-api new -monorepo -dir services/orders` from the
// repo root itself), displayPath returns ".", and the README template
// used to render the silly "cd . && go mod tidy" instead of recognizing
// there's nothing to cd into.
func TestGenerateMonorepoCwdIsRootPrintsNoOpCd(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root, "github.com/acme/monorepo", false)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	serviceDir := filepath.Join(root, "services", "orders")
	spec, err := ParseSpec("orders", "", serviceDir, true, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(serviceDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if strings.Contains(string(readme), "cd . &&") {
		t.Errorf("README.md contains a no-op \"cd . &&\":\n%s", readme)
	}
}

// TestGenerateMonorepoWithExistingGoAPIDep checks the README's
// first-run instructions adapt when the root module already requires
// go-api — no "add the dependency" step to print in that case.
func TestGenerateMonorepoWithExistingGoAPIDep(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root, "github.com/acme/monorepo", true)
	serviceDir := filepath.Join(root, "services", "orders")

	spec, err := ParseSpec("orders", "", serviceDir, true, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	readme, err := os.ReadFile(filepath.Join(serviceDir, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	if strings.Contains(string(readme), "first go-api-based service") {
		t.Error("README.md shouldn't claim this is the first go-api service when the root go.mod already requires it")
	}
}

// TestGenerateMonorepoNoGoModIsAnError checks -monorepo fails clearly,
// rather than silently falling back to standalone behavior, when -dir
// isn't actually inside an existing module.
func TestGenerateMonorepoNoGoModIsAnError(t *testing.T) {
	root := t.TempDir() // deliberately no go.mod anywhere under/above this
	serviceDir := filepath.Join(root, "services", "orders")

	spec, err := ParseSpec("orders", "", serviceDir, true, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err == nil {
		t.Fatal("expected Generate to fail: no go.mod exists above serviceDir")
	}
}

// TestGenerateMonorepoModuleMismatchIsAnError checks an explicit -module
// that disagrees with the detected one is rejected rather than silently
// producing an import path that happens to not match reality.
func TestGenerateMonorepoModuleMismatchIsAnError(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root, "github.com/acme/monorepo", false)
	serviceDir := filepath.Join(root, "services", "orders")

	spec, err := ParseSpec("orders", "github.com/acme/something-else", serviceDir, true, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err == nil {
		t.Fatal("expected Generate to reject a -module that disagrees with the detected go.mod")
	}
}

// TestGenerateMonorepoExplicitModuleMatching checks the opposite: an
// explicit -module that *does* match the detected one is accepted, not
// just tolerated by omitting -module entirely.
func TestGenerateMonorepoExplicitModuleMatching(t *testing.T) {
	root := t.TempDir()
	writeRootGoMod(t, root, "github.com/acme/monorepo", false)
	serviceDir := filepath.Join(root, "services", "orders")

	spec, err := ParseSpec("orders", "github.com/acme/monorepo", serviceDir, true, "hmac", false, false, nil, false, false)
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if _, err := Generate(spec); err != nil {
		t.Fatalf("Generate: %v", err)
	}
}
