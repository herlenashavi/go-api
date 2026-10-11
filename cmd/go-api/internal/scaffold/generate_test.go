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
			spec, err := ParseSpec(c.name, "github.com/acme/"+c.name, dir, string(c.authn), c.authz, c.db, c.cruds, false, false)
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
	spec, err := ParseSpec("svc", "github.com/acme/svc", dir, "hmac", false, true, nil, false, false)
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

	spec, err := ParseSpec("svc", "github.com/acme/svc", dir, "hmac", false, false, nil, false, false)
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
	base, err := ParseSpec("svc", "github.com/acme/svc", dir, "jwt", true, true, []string{"Order"}, false, false)
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
