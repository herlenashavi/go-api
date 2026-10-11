package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// mainData is main.go.tmpl's input. ImportBlock is computed up front (see
// imports.go) rather than left to template conditionals — Go treats an
// unused import as a compile error, so getting that set exactly right is
// the generator's job, not something to leave to per-flag template logic
// that's easy to get subtly wrong in one branch combination and not
// notice until someone actually picks that combination.
type mainData struct {
	ServiceName string
	Authn       string // "none" | "hmac" | "jwt"
	Authz       bool
	DB          bool
	Resources   []Resource
	ImportBlock string
}

type docData struct {
	ServiceName string
	GoVersion   string
	Authn       string
	Authz       bool
	DB          bool
	Resources   []Resource

	// Monorepo-only fields; zero-valued otherwise.
	Monorepo       bool
	RelDir         string // e.g. "services/orders"
	RootDirDisplay string // repo root, relative to cwd if possible
	HasGoAPIDep    bool
}

type migrateData struct {
	// ImportPath is the migrations package's own import path: spec.Module
	// in standalone mode (Dir is the module root), or the detected
	// monorepo module plus Dir's relative position in it otherwise — see
	// monorepoTarget.ImportPath.
	ImportPath  string
	ServiceName string
}

// genFile is one output file: Path relative to the service's root
// directory, Template the base name passed to render, Data whatever that
// template needs.
type genFile struct {
	Path     string
	Template string
	Data     any
}

// Plan returns the full set of files Generate would write, without
// touching disk other than the read-only go.mod lookup -monorepo mode
// needs (see resolveMonorepo) — Generate's dry-run mode and its real
// write path share this so "what would be created" and "what is
// created" can never drift apart.
func Plan(spec Spec) ([]genFile, error) {
	goVersion := goVersion()

	importPath := spec.Module // standalone: Dir is the module root, Module names it directly
	doc := docData{
		ServiceName: spec.Name,
		GoVersion:   goVersion,
		Authn:       string(spec.Authn),
		Authz:       spec.Authz,
		DB:          spec.DB,
		Resources:   spec.Resources,
	}

	if spec.Monorepo {
		target, err := resolveMonorepo(spec.Dir, spec.Module)
		if err != nil {
			return nil, fmt.Errorf("-monorepo: %w", err)
		}
		importPath = target.ImportPath
		doc.Monorepo = true
		doc.RelDir = target.RelDir
		doc.RootDirDisplay = displayPath(target.RootDir)
		doc.HasGoAPIDep = target.HasGoAPIDep
	}

	var files []genFile
	if !spec.Monorepo {
		files = append(files,
			genFile{"go.mod", "gomod", map[string]string{"Module": spec.Module, "GoVersion": goVersion}},
			genFile{".gitignore", "gitignore", map[string]string{"ServiceName": spec.Name}},
		)
	}
	files = append(files,
		genFile{"Dockerfile", "dockerfile", docData{
			ServiceName: spec.Name, GoVersion: goVersion, Authz: spec.Authz,
			Monorepo: spec.Monorepo, RelDir: doc.RelDir,
		}},
		genFile{"README.md", "readme.md", doc},
		genFile{"main.go", "main.go", mainData{
			ServiceName: spec.Name,
			Authn:       string(spec.Authn),
			Authz:       spec.Authz,
			DB:          spec.DB,
			Resources:   spec.Resources,
			ImportBlock: mainImportBlock(spec),
		}},
	)

	if spec.Authn == AuthnHMAC {
		files = append(files, genFile{"dev_token.go", "dev_token.go", nil})
	}

	if spec.Authz {
		files = append(files, genFile{"policies/" + spec.Name + ".cedar", "policy.cedar", map[string]any{
			"ServiceName": spec.Name,
			"Resources":   spec.Resources,
		}})
	}

	if spec.DB {
		files = append(files,
			genFile{"migrations/migrations.go", "migrations_pkg.go", map[string]string{"ServiceName": spec.Name}},
			genFile{"migrate/main.go", "migrate_main.go", migrateData{ImportPath: importPath, ServiceName: spec.Name}},
		)
		if len(spec.Resources) == 0 {
			// migrations.go's //go:embed *.sql requires at least one match —
			// with no -crud resources there's nothing else to migrate yet,
			// so seed a genuine no-op rather than leave the embed directive
			// broken until the first resource/hand-written migration exists.
			files = append(files,
				genFile{"migrations/0001_init.up.sql", "migration.init.sql", nil},
				genFile{"migrations/0001_init.down.sql", "migration.init.sql", nil},
			)
		}
		for i, r := range spec.Resources {
			n := fmt.Sprintf("%04d", i+1)
			files = append(files,
				genFile{"migrations/" + n + "_create_" + r.Plural + ".up.sql", "migration.up.sql", r},
				genFile{"migrations/" + n + "_create_" + r.Plural + ".down.sql", "migration.down.sql", r},
			)
		}
	}

	for _, r := range spec.Resources {
		files = append(files,
			genFile{r.Lower + ".go", "model.go", r},
			genFile{"store_" + r.Lower + ".go", "store_pg.go", r},
		)
	}

	return files, nil
}

// Generate writes spec's plan to spec.Dir. With spec.DryRun set, it only
// returns the list of paths that would be written (via the returned
// []string) and touches nothing beyond Plan's own read-only lookups.
func Generate(spec Spec) ([]string, error) {
	plan, err := Plan(spec)
	if err != nil {
		return nil, err
	}
	paths := make([]string, len(plan))
	for i, f := range plan {
		paths[i] = f.Path
	}
	if spec.DryRun {
		return paths, nil
	}

	if err := checkTargetDir(spec.Dir, spec.Force); err != nil {
		return nil, err
	}

	for _, f := range plan {
		content, err := render(f.Template, f.Data)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		fullPath := filepath.Join(spec.Dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			return nil, fmt.Errorf("create directory for %s: %w", f.Path, err)
		}
		if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
			return nil, fmt.Errorf("write %s: %w", f.Path, err)
		}
	}
	return paths, nil
}

// displayPath renders dir relative to the current working directory —
// since dir is always an ancestor of the generated service's own
// directory (resolveMonorepo only ever walks upward to find it), this is
// almost always a short "../.." a reader can actually act on, versus an
// absolute path. Falls back to the absolute path only if Getwd/Rel
// itself fails (e.g. a different drive on Windows).
func displayPath(dir string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return dir
	}
	rel, err := filepath.Rel(cwd, dir)
	if err != nil {
		return dir
	}
	return rel
}

// checkTargetDir refuses to write into a non-empty directory unless force
// is set — the same "don't silently clobber work that might already be
// there" rule as everything else in this repo that touches the
// filesystem destructively.
func checkTargetDir(dir string, force bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return os.MkdirAll(dir, 0o755)
		}
		return fmt.Errorf("read %s: %w", dir, err)
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("%s already exists and is not empty (use -force to write into it anyway)", dir)
	}
	return nil
}

// mainImportBlock computes exactly the import set main.go.tmpl's rendered
// body references for this spec — see mainData's doc comment on why this
// is computed in Go rather than left to the template.
func mainImportBlock(spec Spec) string {
	stdlib := []string{"context", "log/slog", "os", "os/signal", "syscall"}
	if spec.DB {
		stdlib = append(stdlib, "fmt")
	}
	if spec.Authz {
		stdlib = append(stdlib, "time")
	}
	if spec.Authn == AuthnHMAC {
		stdlib = append(stdlib, "net/http")
	}

	rest := []string{GoAPIModule + "/pkg/app", GoAPIModule + "/pkg/config"}
	if spec.Authn != AuthnNone {
		rest = append(rest, GoAPIModule+"/pkg/authn")
	}
	if spec.Authn == AuthnHMAC {
		rest = append(rest, "github.com/go-chi/chi/v5", GoAPIModule+"/pkg/httpx")
	}
	if spec.Authz {
		rest = append(rest, GoAPIModule+"/pkg/authz")
	}
	if len(spec.Resources) > 0 {
		rest = append(rest, GoAPIModule+"/pkg/crud")
	}
	if spec.DB {
		rest = append(rest, GoAPIModule+"/pkg/dbx", GoAPIModule+"/pkg/health")
	}
	return buildImportBlock(stdlib, rest)
}

// goVersion reports the running toolchain's major.minor version (e.g.
// "1.23" from "go1.23.4") — the generated go.mod's `go` directive and
// Dockerfile base image use whatever Go actually built this binary,
// rather than a version pinned into the template that would drift from
// reality the next time the Go toolchain itself is upgraded.
func goVersion() string {
	v := strings.TrimPrefix(runtime.Version(), "go")
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return "1.23"
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return "1.23"
	}
	return parts[0] + "." + parts[1]
}
