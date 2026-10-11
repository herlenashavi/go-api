package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/herlenashavi/go-api/cmd/go-api/internal/scaffold"
)

// stringSlice collects repeated occurrences of a flag (e.g. multiple
// -crud Order -crud LineItem) — the standard library's flag package has
// no built-in support for that.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func runNew(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("usage: go-api new <name> -module <path> [flags] (run \"go-api new -h\" for the full flag list)")
	}
	name := args[0]
	args = args[1:]

	fs := flag.NewFlagSet("new", flag.ExitOnError)
	module := fs.String("module", "", "Go module path for the generated service (required unless -monorepo)")
	dir := fs.String("dir", "", "target directory (default: ./<name>)")
	monorepo := fs.Bool("monorepo", false, "generate into the existing module enclosing -dir instead of a new standalone one — no go.mod/.gitignore, Dockerfile/migrate import paths shaped for a repo-root build context")
	authn := fs.String("authn", "hmac", "authn.Verifier to wire: none, hmac, or jwt")
	authz := fs.Bool("authz", false, "wire a Cedar authz.Authorizer with a starter policy")
	db := fs.Bool("db", false, "wire a Postgres dbx.Pool, health check, and migrations")
	force := fs.Bool("force", false, "overwrite a non-empty target directory")
	dryRun := fs.Bool("dry-run", false, "print the files that would be generated; write nothing")
	var cruds stringSlice
	fs.Var(&cruds, "crud", "generate a CRUD resource with this name (repeatable; requires -db)")

	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `Usage: go-api new <name> -module <path> [flags]

Scaffolds a new service directory: its own go.mod requiring
github.com/herlenashavi/go-api, wired up per the flags below — or, with
-monorepo, generated into the module enclosing -dir instead. See
docs/scaffolding.md for what's generated and what isn't yet
(gRPC/mTLS/Tilt/the custom-metrics example).

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		return fmt.Errorf("unexpected extra argument(s): %v", fs.Args())
	}

	spec, err := scaffold.ParseSpec(name, *module, *dir, *monorepo, *authn, *authz, *db, cruds, *force, *dryRun)
	if err != nil {
		return err
	}

	paths, err := scaffold.Generate(spec)
	if err != nil {
		return err
	}

	if spec.DryRun {
		fmt.Printf("Would generate %d files in %s:\n", len(paths), spec.Dir)
	} else {
		fmt.Printf("Generated %d files in %s:\n", len(paths), spec.Dir)
	}
	for _, p := range paths {
		fmt.Println("  " + p)
	}
	if !spec.DryRun {
		if spec.Monorepo {
			fmt.Printf("\nNext steps: see %s/README.md (first-run instructions differ here since there's no new go.mod).\n", spec.Dir)
		} else {
			fmt.Printf("\nNext steps:\n  cd %s\n  go mod tidy   # needs network access to resolve github.com/herlenashavi/go-api\n  go build ./...\n", spec.Dir)
		}
	}
	return nil
}
