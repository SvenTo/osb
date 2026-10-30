// Package imports resolves git imports and maintains the lockfile.
package imports

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	gitcfg "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"go.yaml.in/yaml/v3"

	"github.com/SvenTo/osb/internal/config"
)

// LockFile is the name of the lockfile next to the root configuration.
const LockFile = "osb.lock"

// Lock pins git imports to commits.
type Lock struct {
	Imports map[string]LockEntry `yaml:"imports"`
}

// LockEntry is one pinned import.
type LockEntry struct {
	Commit string `yaml:"commit"`
}

// Resolver resolves git imports using a cache directory and a lockfile.
type Resolver struct {
	// LockPath is the lockfile path; empty disables locking.
	LockPath string
	// CacheDir holds checkouts.
	CacheDir string
	// Update re-resolves refs and rewrites the lockfile.
	Update bool
	// Log receives progress messages.
	Log func(string)

	lock    *Lock
	used    map[string]bool
	changed bool
}

// DefaultCacheDir returns $XDG_CACHE_HOME/osb.
func DefaultCacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	return filepath.Join(d, "osb")
}

// NewResolver creates a resolver for a root configuration file.
func NewResolver(configFile string, update bool) *Resolver {
	abs, _ := filepath.Abs(configFile)
	return &Resolver{LockPath: filepath.Join(filepath.Dir(abs), LockFile), CacheDir: DefaultCacheDir(), Update: update}
}

func (r *Resolver) log(format string, a ...any) {
	if r.Log != nil {
		r.Log(fmt.Sprintf(format, a...))
	}
}

func (r *Resolver) load() error {
	if r.lock != nil {
		return nil
	}
	r.lock = &Lock{Imports: map[string]LockEntry{}}
	r.used = map[string]bool{}
	if r.LockPath == "" {
		return nil
	}
	b, err := os.ReadFile(r.LockPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := yaml.Unmarshal(b, r.lock); err != nil {
		return fmt.Errorf("%s: %w", r.LockPath, err)
	}
	if r.lock.Imports == nil {
		r.lock.Imports = map[string]LockEntry{}
	}
	return nil
}

// Key identifies an import in the lockfile.
func Key(imp config.Import) string {
	ref := imp.Ref
	if ref == "" {
		ref = "HEAD"
	}
	return imp.Git + "@" + ref
}

func isLocal(url string) bool {
	return !strings.Contains(url, "://") && !strings.HasPrefix(url, "git@")
}

// Resolve returns the checkout directory for a git import (config.LoadOptions.ResolveGit).
func (r *Resolver) Resolve(imp config.Import, from string) (string, error) {
	if err := r.load(); err != nil {
		return "", err
	}
	url := imp.Git
	if isLocal(url) && !filepath.IsAbs(url) {
		url = filepath.Join(from, url)
	}
	key := Key(imp)
	r.used[key] = true
	sum := sha256.Sum256([]byte(url))
	repoDir := filepath.Join(r.CacheDir, "git", hex.EncodeToString(sum[:])[:16])

	commit := ""
	if e, ok := r.lock.Imports[key]; ok && !r.Update {
		commit = e.Commit
	}
	if commit != "" {
		dir := filepath.Join(repoDir, commit)
		if _, err := os.Stat(filepath.Join(dir, ".osb-complete")); err == nil {
			return dir, nil
		}
	}
	// Clone (or refresh) a mirror and resolve the ref.
	mirror := filepath.Join(repoDir, "mirror")
	repo, err := git.PlainOpen(mirror)
	if err != nil {
		r.log("cloning %s", imp.Git)
		_ = os.RemoveAll(mirror)
		repo, err = git.PlainClone(mirror, true, &git.CloneOptions{URL: url, Tags: git.AllTags})
		if err != nil {
			return "", fmt.Errorf("clone %s: %w", imp.Git, err)
		}
	} else if commit == "" || !hasCommit(repo, commit) {
		r.log("fetching %s", imp.Git)
		err := repo.Fetch(&git.FetchOptions{RefSpecs: []gitcfg.RefSpec{"+refs/heads/*:refs/heads/*", "+refs/tags/*:refs/tags/*"}, Tags: git.AllTags, Force: true})
		if err != nil && err != git.NoErrAlreadyUpToDate {
			return "", fmt.Errorf("fetch %s: %w", imp.Git, err)
		}
	}
	if commit == "" {
		h, err := resolveRef(repo, imp.Ref)
		if err != nil {
			return "", fmt.Errorf("%s: %w", imp.Git, err)
		}
		commit = h.String()
		if prev, ok := r.lock.Imports[key]; !ok || prev.Commit != commit {
			r.lock.Imports[key] = LockEntry{Commit: commit}
			r.changed = true
			r.log("locked %s to %s", key, commit[:12])
		}
	}
	dir := filepath.Join(repoDir, commit)
	if _, err := os.Stat(filepath.Join(dir, ".osb-complete")); err == nil {
		return dir, nil
	}
	_ = os.RemoveAll(dir)
	wt, err := git.PlainClone(dir, false, &git.CloneOptions{URL: mirror, NoCheckout: true})
	if err != nil {
		return "", fmt.Errorf("checkout %s: %w", imp.Git, err)
	}
	w, err := wt.Worktree()
	if err != nil {
		return "", err
	}
	if err := w.Checkout(&git.CheckoutOptions{Hash: plumbing.NewHash(commit), Force: true}); err != nil {
		return "", fmt.Errorf("checkout %s@%s: %w", imp.Git, commit, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".osb-complete"), nil, 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func hasCommit(repo *git.Repository, commit string) bool {
	_, err := repo.CommitObject(plumbing.NewHash(commit))
	return err == nil
}

func resolveRef(repo *git.Repository, ref string) (*plumbing.Hash, error) {
	if ref == "" {
		ref = "HEAD"
	}
	for _, cand := range []string{ref, "refs/tags/" + ref, "refs/heads/" + ref, "refs/remotes/origin/" + ref} {
		h, err := repo.ResolveRevision(plumbing.Revision(cand))
		if err == nil {
			// Tags may point to tag objects; resolve to the commit.
			if tag, err := repo.TagObject(*h); err == nil {
				c, err := tag.Commit()
				if err == nil {
					hh := c.Hash
					return &hh, nil
				}
			}
			return h, nil
		}
	}
	return nil, fmt.Errorf("cannot resolve ref %q", ref)
}

// Save writes the lockfile if it changed. With prune, entries not used in
// this run are removed.
func (r *Resolver) Save(prune bool) error {
	if r.lock == nil || r.LockPath == "" {
		return nil
	}
	if prune {
		for k := range r.lock.Imports {
			if !r.used[k] {
				delete(r.lock.Imports, k)
				r.changed = true
			}
		}
	}
	if !r.changed {
		return nil
	}
	keys := make([]string, 0, len(r.lock.Imports))
	for k := range r.lock.Imports {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# osb lockfile: pins git imports to commits. Update with `osb imports update`.\nimports:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "  %q:\n    commit: %s\n", k, r.lock.Imports[k].Commit)
	}
	if len(keys) == 0 {
		b.Reset()
		b.WriteString("# osb lockfile: pins git imports to commits. Update with `osb imports update`.\nimports: {}\n")
	}
	r.changed = false
	return os.WriteFile(r.LockPath, []byte(b.String()), 0o644)
}

// Entries returns the locked imports.
func (r *Resolver) Entries() map[string]LockEntry {
	_ = r.load()
	return r.lock.Imports
}
