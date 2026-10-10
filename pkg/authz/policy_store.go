package authz

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

// PolicyStore holds the current, parsed set of Cedar policies and supports
// safe hot reload: Authorizer always reads the latest snapshot, even while a
// reload is in progress, via an atomically-swapped pointer — no locks on the
// authorization hot path.
type PolicyStore struct {
	dir     string
	current atomic.Pointer[cedar.PolicySet]
}

// LoadPolicyDir reads every "*.cedar" file in dir (non-recursive) and
// compiles them into one PolicySet. Each file may contain multiple policy
// statements; policy IDs are derived as "<filename>#<index>" so two files
// can't collide and a parse error always names the exact file at fault.
//
// It fails fast on any parse error — an unparseable policy file is a
// deploy-time mistake, not something to silently skip.
func LoadPolicyDir(dir string) (*PolicyStore, error) {
	s := &PolicyStore{dir: dir}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	return s, nil
}

// Reload re-reads every policy file and atomically swaps in the new set.
// Existing in-flight requests keep using whichever snapshot they already
// read; new requests see the update immediately after Reload returns.
func (s *PolicyStore) Reload() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return fmt.Errorf("authz: read policy dir %q: %w", s.dir, err)
	}

	var files []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".cedar" {
			continue
		}
		files = append(files, e.Name())
	}
	sort.Strings(files) // deterministic policy IDs across reloads

	ps := cedar.NewPolicySet()
	for _, name := range files {
		path := filepath.Join(s.dir, name)
		document, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("authz: read policy file %q: %w", path, err)
		}
		policies, err := cedar.NewPolicyListFromBytes(name, document)
		if err != nil {
			return fmt.Errorf("authz: parse policy file %q: %w", path, err)
		}
		for i, p := range policies {
			id := cedar.PolicyID(fmt.Sprintf("%s#%d", name, i))
			ps.Add(id, p)
		}
	}

	s.current.Store(ps)
	return nil
}

// Set returns the current, immutable PolicySet snapshot.
func (s *PolicyStore) Set() *cedar.PolicySet {
	return s.current.Load()
}

// WatchAndReload polls the policy directory every interval and reloads
// whenever any "*.cedar" file's modification time has changed, until ctx is
// canceled. It never swaps in a broken reload: a parse error is logged and
// the previous, known-good PolicySet keeps serving traffic.
//
// This is a simple polling watcher rather than an inotify/fsnotify-based one
// so go-api doesn't force that dependency on services that don't need hot
// reload at all — most deployments just restart on policy change and never
// call this.
func (s *PolicyStore) WatchAndReload(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	lastModTime := s.dirModTime()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			modTime := s.dirModTime()
			if modTime.Equal(lastModTime) {
				continue
			}
			lastModTime = modTime
			if err := s.Reload(); err != nil {
				logger.Error("authz: policy reload failed, keeping previous policies", "error", err, "dir", s.dir)
				continue
			}
			logger.Info("authz: policies reloaded", "dir", s.dir)
		}
	}
}

// dirModTime returns the most recent modification time among the policy
// directory's *.cedar files (and the directory itself, to catch deletes).
func (s *PolicyStore) dirModTime() time.Time {
	latest := time.Time{}
	if info, err := os.Stat(s.dir); err == nil && info.ModTime().After(latest) {
		latest = info.ModTime()
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return latest
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".cedar" {
			continue
		}
		if info, err := e.Info(); err == nil && info.ModTime().After(latest) {
			latest = info.ModTime()
		}
	}
	return latest
}
