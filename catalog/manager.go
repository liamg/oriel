package catalog

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
)

// Badges say who looked at a plugin's code. Consent is the same whatever the badge.
const (
	BadgeBuiltin    = "builtin"
	BadgeBundled    = "bundled" // shipped in oriel's binary, as a plugin of its own
	BadgeReviewed   = "reviewed"
	BadgeTrusted    = "trusted"
	BadgeUnverified = "unverified"
)

// UnverifiedWarning is what the install screen says of a git or path source.
const UnverifiedWarning = "Not in any registry. Nobody has reviewed this code."

// State is where a plugin in [plugins] stands.
type State int

const (
	Ready    State = iota // installed and consented to: load it
	Consent               // installed, but asking for more than the lockfile covers: inactive until you accept
	Disabled              // enabled = false
	Failed                // couldn't be resolved, fetched or read
	Revoked               // the version installed was revoked by its registry: disabled
)

func (s State) String() string {
	return [...]string{"ready", "consent", "disabled", "failed", "revoked"}[s]
}

// Status is one [plugins] entry, resolved.
type Status struct {
	Entry    Entry
	State    State
	Err      error
	Dir      string // where it's installed
	Manifest plugin.Manifest
	From     *plugin.Manifest // what the lockfile had, when this is an update of it
	FromDir  string           // where that's installed
	Locked   Locked           // what the lockfile will say once it's consented to
	Facts    []plugin.Fact    // its provenance, for the install screen
	Warning  string
	Update   string // a newer version the registry offers, not taken yet
}

// Grants are what it's granted once loaded: what its manifest declares, all of which you accepted.
func (s Status) Grants() []plugin.Grant { return s.Manifest.Permissions }

// Ask is the consent screen for it.
func (s Status) Ask() plugin.Consent {
	c := plugin.Ask(s.Manifest, s.From)
	c.Source, c.Facts, c.Badge, c.Warning = s.Locked.Source, s.Facts, s.Locked.Badge, s.Warning
	return c
}

// Resolver finds a registry entry's version: what to install, and from where.
type Resolver interface {
	Resolve(ctx context.Context, e Entry, locked *Locked) (Resolution, error)
}

// Resolution is a registry plugin's version to install.
type Resolution struct {
	Locked        // source, version, commit, badge and archives; Capabilities empty
	Repo   string // the git repository, cloned when there's no archive for this platform
	// Fetch downloads this platform's archive, checked against Locked.Archives, and says its file and sha256; ""
	// when there's none, and the repo is cloned at the commit instead. Nil for no archive.
	Fetch    func(ctx context.Context) (file, sha256 string, err error)
	Facts    []plugin.Fact
	Revoked  string // why the locked version was revoked: it isn't loaded
	Update   string // a newer version in the range, not taken: [registry] updates isn't auto
	Platform string
}

// Manager installs what one settings.toml's [plugins] lists, keeps its lockfile, and says what may load.
type Manager struct {
	Store    Store
	Lock     string   // the lockfile's path: oriel-lock.toml next to the settings.toml
	Resolver Resolver // registry entries; they fail without one
}

// Sync resolves and fetches each entry, and says where each stands. A plugin the lockfile covers (this source,
// and every capability it asks for accepted) is Ready; one asking for more waits for consent. Entries the
// lockfile has that the file no longer lists are uninstalled: their files go and their lock entries are dropped,
// and their ids are returned in removed.
func (m *Manager) Sync(ctx context.Context, entries []Entry, keep func(id string) bool) (sts []Status, removed []string, err error) {
	lock, err := LoadLock(m.Lock)
	if err != nil {
		return nil, nil, err
	}
	listed := map[string]bool{}
	for _, e := range entries {
		listed[e.ID] = true
		sts = append(sts, m.resolve(ctx, lock, e))
	}
	for _, id := range lock.IDs() {
		if listed[id] {
			continue
		}
		lock.Delete(id)
		if keep == nil || !keep(id) {
			m.Store.Remove(id)
		}
		removed = append(removed, id)
	}
	for _, s := range sts {
		if s.State == Ready {
			// The commit can move without asking again (a path source, the same capabilities).
			lock.Set(s.Entry.ID, s.Locked)
		}
	}
	return sts, removed, lock.Save()
}

func (m *Manager) resolve(ctx context.Context, lock *Lock, e Entry) Status {
	st := Status{Entry: e}
	fail := func(err error) Status {
		st.State, st.Err = Failed, err
		return st
	}
	locked, hasLock := lock.Get(e.ID)
	if hasLock && !sameSource(locked.Source, e) {
		hasLock = false
	}
	switch {
	case e.Git != "":
		rev := e.Rev
		if hasLock && locked.Commit != "" {
			rev = locked.Commit
		}
		dir, commit, err := m.Store.FetchGit(ctx, e.ID, e.GitURL(), rev)
		if err != nil {
			return fail(err)
		}
		st.Dir = dir
		st.Locked = Locked{Source: e.Source(), Commit: commit, Badge: BadgeUnverified}
		st.Facts = []plugin.Fact{{Label: "commit", Value: short(commit)}, {Label: "badge", Value: BadgeUnverified}}
		st.Warning = UnverifiedWarning
	case e.Path != "":
		st.Dir = plugin.Expand(e.Path)
		if _, err := os.Stat(st.Dir); err != nil {
			return fail(err)
		}
		st.Locked = Locked{Source: e.Source(), Badge: BadgeUnverified}
		if c, err := (gitx.Repo{Dir: st.Dir}).Head(ctx); err == nil && gitx.IsRepo(st.Dir) {
			st.Locked.Commit = c
		}
		st.Facts = []plugin.Fact{{Label: "path", Value: e.Path}, {Label: "badge", Value: BadgeUnverified}}
		st.Warning = UnverifiedWarning
	default:
		if m.Resolver == nil {
			return fail(fmt.Errorf("%s: no plugin registry to resolve it from", e.ID))
		}
		var lp *Locked
		if hasLock {
			lp = &locked
		}
		r, err := m.Resolver.Resolve(ctx, e, lp)
		if err != nil {
			return fail(err)
		}
		if r.Revoked != "" {
			st.Locked, st.State, st.Err = r.Locked, Revoked, fmt.Errorf("%s %s was revoked: %s", e.ID, r.Version, r.Revoked)
			return st
		}
		var dir string
		file := ""
		if !m.Store.Has(e.ID, r.Commit) && r.Fetch != nil {
			var sum string
			if file, sum, err = r.Fetch(ctx); err != nil {
				return fail(err)
			}
			if file != "" {
				defer os.Remove(file)
				r.Archives = map[string]string{r.Platform: sum}
			}
		}
		if file != "" {
			dir, err = m.Store.FetchArchive(e.ID, r.Commit, file)
		} else {
			dir, _, err = m.Store.FetchGit(ctx, e.ID, r.Repo, r.Commit)
		}
		if err != nil {
			return fail(err)
		}
		st.Dir, st.Locked, st.Facts, st.Update = dir, r.Locked, r.Facts, r.Update
	}
	man, err := plugin.LoadManifest(st.Dir)
	if err != nil {
		return fail(err)
	}
	if man.ID != e.ID {
		return fail(fmt.Errorf("%s is %s, not %s", st.Dir, man.ID, e.ID))
	}
	if e.Registry() && len(man.Binaries) > 0 {
		if _, err := os.Stat(filepath.Join(st.Dir, "bin")); err != nil {
			return fail(fmt.Errorf("%s %s has no archive for this platform, and its binaries aren't in its source", e.ID, man.Version))
		}
	}
	if e.Git != "" && len(man.Binaries) > 0 {
		return fail(fmt.Errorf("%s ships process binaries, which come from a registry's release archives: install it from a registry, or from a path where you've built it", e.ID))
	}
	if e.Lazy != nil {
		man.Lazy = *e.Lazy
	}
	st.Manifest = man
	if st.Locked.Version == "" {
		st.Locked.Version = man.Version
	}
	want := plugin.Locked(man.Permissions)
	st.Locked.Capabilities = want
	switch {
	case !e.Enabled:
		st.State = Disabled
	case len(plugin.Diff(want, lockedCaps(hasLock, locked))) == 0:
		// The lockfile covers it, or it asks for nothing consent covers.
		st.State = Ready
	default:
		st.State = Consent
		if hasLock && locked.Commit != "" && locked.Commit != st.Locked.Commit && m.Store.Has(e.ID, locked.Commit) {
			if old, err := plugin.LoadManifest(m.Store.Path(e.ID, locked.Commit)); err == nil {
				st.From, st.FromDir = &old, m.Store.Path(e.ID, locked.Commit)
			}
		}
	}
	return st
}

func lockedCaps(has bool, l Locked) []string {
	if !has {
		return nil
	}
	return l.Capabilities
}

// sameSource says whether a lock entry's source is e's: a registry entry's is whichever registry it was found in.
func sameSource(locked string, e Entry) bool {
	if e.Registry() {
		return strings.HasPrefix(locked, "registry")
	}
	return locked == e.Source()
}

// Accept records consent to st: its lock entry, with the capabilities it asks for now.
func (m *Manager) Accept(st Status) error {
	lock, err := LoadLock(m.Lock)
	if err != nil {
		return err
	}
	if err := lock.Set(st.Entry.ID, st.Locked); err != nil {
		return err
	}
	return lock.Save()
}

func short(commit string) string { return commit[:min(7, len(commit))] }
