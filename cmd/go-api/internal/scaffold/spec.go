// Package scaffold generates a new go-api service directory: a standalone
// Go module with its own go.mod requiring github.com/herlenashavi/go-api,
// wired up per the flags on `go-api new` (pkg/dbx for storage, authn/authz,
// one or more pkg/crud resources). It is deliberately not a general
// templating engine — see docs/scaffolding.md for which add-ons exist
// today and which are still roadmap items (GRPCEnabled/mTLS/Tilt/the
// custom-metrics example, and `go-api add resource` for an existing
// service) rather than silently half-wired.
package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

// AuthnMode selects which authn.Verifier main.go constructs.
type AuthnMode string

const (
	AuthnNone AuthnMode = "none"
	AuthnHMAC AuthnMode = "hmac"
	AuthnJWT  AuthnMode = "jwt"
)

// GoAPIModule is this framework's own module path — every generated
// service imports pkg/* from here, pinned to whatever version `go mod
// tidy` resolves (see docs/scaffolding.md's "network access" note).
const GoAPIModule = "github.com/herlenashavi/go-api"

var serviceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// Spec is the fully validated input to Generate — everything `new`'s flags
// decided, with no further validation needed once one of these exists.
type Spec struct {
	// Name is the service's identifier: its directory's base name (unless
	// overridden by --dir), SERVICE_NAME default, and the base for
	// generated Cedar policy/README text.
	Name string
	// Module is the generated go.mod's module path in standalone mode —
	// required there, since guessing a default (e.g. from $GOPATH or a
	// VCS remote) would be wrong often enough to not be worth the
	// convenience. In -monorepo mode there is no go.mod to name, so this
	// is optional: left empty, the module enclosing Dir is detected by
	// walking up to its nearest go.mod; given anyway, it's checked
	// against that detected module and rejected on a mismatch rather
	// than silently producing imports for a module Dir isn't actually
	// inside — see monorepo.go's resolveMonorepo.
	Module string
	// Dir is the target directory, created if it doesn't exist.
	Dir string
	// Monorepo generates into an existing module (detected from Dir's
	// position in it) instead of scaffolding a new standalone one: no
	// go.mod/.gitignore, and the Dockerfile/migrate package import paths
	// are shaped for a repo-root build context instead of Dir being the
	// module root. See docs/scaffolding.md.
	Monorepo bool

	Authn AuthnMode
	Authz bool
	DB    bool

	Resources []Resource

	Force  bool // overwrite a non-empty Dir
	DryRun bool // print the file list, write nothing
}

// ParseSpec validates raw flag values into a Spec. It is separate from
// flag parsing itself (see cmd/go-api/new.go) so the validation rules
// are testable without going through os.Args. It does no filesystem
// I/O — monorepo module detection needs that, and happens later, in
// Plan (see resolveMonorepo), where Generate's dry-run mode can still
// observe it without writing anything.
func ParseSpec(name, module, dir string, monorepo bool, authn string, authz, db bool, cruds []string, force, dryRun bool) (Spec, error) {
	if !serviceNamePattern.MatchString(name) {
		return Spec{}, fmt.Errorf("service name %q must start with a lowercase letter and contain only lowercase letters, digits, and hyphens", name)
	}
	if !monorepo && strings.TrimSpace(module) == "" {
		return Spec{}, fmt.Errorf("-module is required (e.g. -module github.com/yourorg/%s), unless -monorepo is set", name)
	}
	if dir == "" {
		dir = "./" + name
	}

	mode := AuthnMode(authn)
	switch mode {
	case AuthnNone, AuthnHMAC, AuthnJWT:
	default:
		return Spec{}, fmt.Errorf("-authn must be one of %q, %q, %q (got %q)", AuthnNone, AuthnHMAC, AuthnJWT, authn)
	}

	resources := make([]Resource, 0, len(cruds))
	seen := make(map[string]bool, len(cruds))
	for _, raw := range cruds {
		res, err := NewResource(raw)
		if err != nil {
			return Spec{}, err
		}
		if seen[res.Name] {
			return Spec{}, fmt.Errorf("-crud %q given more than once", res.Name)
		}
		seen[res.Name] = true
		resources = append(resources, res)
	}

	if len(resources) > 0 && !db {
		return Spec{}, fmt.Errorf("-crud requires -db: generated resources are Postgres-backed only (see docs/scaffolding.md)")
	}

	return Spec{
		Name:      name,
		Module:    module,
		Dir:       dir,
		Monorepo:  monorepo,
		Authn:     mode,
		Authz:     authz,
		DB:        db,
		Resources: resources,
		Force:     force,
		DryRun:    dryRun,
	}, nil
}
