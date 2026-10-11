package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// monorepoTarget is everything -monorepo mode needs, derived once from
// where spec.Dir sits inside an existing module tree — there is no
// nested go.mod to anchor an import prefix the way standalone mode's
// Module field does, so this is computed instead of asked for.
type monorepoTarget struct {
	ImportPath  string // e.g. "github.com/acme/monorepo/services/orders"
	RootDir     string // absolute path containing the enclosing go.mod
	RelDir      string // spec.Dir's path relative to RootDir, slash-form
	HasGoAPIDep bool   // whether the root module already requires go-api
}

// resolveMonorepo walks up from dir (which need not exist yet — only its
// ancestors do) to the nearest go.mod, the same module boundary `go
// build` itself uses, and derives everything -monorepo mode needs from
// it. If explicitModule was also given (via -module), it must match the
// detected module path exactly: a deliberate mismatch isn't supported —
// see spec.go's doc comment on Module for why.
func resolveMonorepo(dir, explicitModule string) (monorepoTarget, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return monorepoTarget{}, fmt.Errorf("resolve %s: %w", dir, err)
	}

	mf, rootDir, err := findEnclosingModule(absDir)
	if err != nil {
		return monorepoTarget{}, err
	}
	rootModule := mf.Module.Mod.Path

	if explicitModule != "" && explicitModule != rootModule {
		return monorepoTarget{}, fmt.Errorf(
			"-module %q does not match the module declared in %s (%q) — "+
				"-monorepo derives import paths from the enclosing go.mod, so "+
				"these can't disagree; omit -module to use the detected one",
			explicitModule, filepath.Join(rootDir, "go.mod"), rootModule)
	}

	relDir, err := filepath.Rel(rootDir, absDir)
	if err != nil {
		return monorepoTarget{}, fmt.Errorf("compute %s relative to %s: %w", absDir, rootDir, err)
	}
	relDir = filepath.ToSlash(relDir)

	hasDep := false
	for _, r := range mf.Require {
		if r.Mod.Path == GoAPIModule {
			hasDep = true
			break
		}
	}

	return monorepoTarget{
		ImportPath:  rootModule + "/" + relDir,
		RootDir:     rootDir,
		RelDir:      relDir,
		HasGoAPIDep: hasDep,
	}, nil
}

// findEnclosingModule walks up from absDir looking for the nearest
// go.mod. absDir itself is checked first, so a dir that already has one
// (unusual for -monorepo, but not invalid) resolves to itself.
func findEnclosingModule(absDir string) (*modfile.File, string, error) {
	for candidate := absDir; ; {
		gomod := filepath.Join(candidate, "go.mod")
		data, err := os.ReadFile(gomod)
		if err == nil {
			mf, err := modfile.Parse(gomod, data, nil)
			if err != nil {
				return nil, "", fmt.Errorf("parse %s: %w", gomod, err)
			}
			if mf.Module == nil {
				return nil, "", fmt.Errorf("%s has no module directive", gomod)
			}
			return mf, candidate, nil
		}
		if !os.IsNotExist(err) {
			return nil, "", fmt.Errorf("read %s: %w", gomod, err)
		}

		parent := filepath.Dir(candidate)
		if parent == candidate {
			return nil, "", fmt.Errorf(
				"-monorepo requires -dir to be inside an existing Go module; "+
					"no go.mod found walking up from %s", absDir)
		}
		candidate = parent
	}
}
