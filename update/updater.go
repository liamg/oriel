package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// Updater is the app's side of updating: it finds releases and installs this copy of Oriel. It keeps no UI; the app
// and `oriel update` drive it.
type Updater struct {
	Source  Source
	Keys    []ed25519.PublicKey
	Version string // the running version
	// Installer replaces this copy; nil when it can't be (a build run from a checkout).
	Installer *Installer
	Kind      string // this copy's artifact kind: KindBundle for the macOS .app, KindBinary if ""
	Platform  string // Platform() if ""
	CacheDir  string // downloads
	// Progress is told how a download or an install is getting on.
	Progress Progress

	mu      sync.Mutex
	rel     *Release
	m       Manifest
	changes []Manifest
}

// ErrNoRelease is asking to install before a release was found.
var ErrNoRelease = errors.New("no update has been found")

// Check asks the source for channel's newest release and keeps it if it verifies and is newer than this copy.
func (u *Updater) Check(ctx context.Context, channel string) (Manifest, error) {
	rel, m, err := Check(ctx, u.Source, u.Keys, channel, u.Version)
	if err != nil {
		return Manifest{}, err
	}
	changes := Changes(ctx, u.Source, u.Keys, channel, u.Version, m)
	u.mu.Lock()
	u.rel, u.m, u.changes = &rel, m, changes
	u.mu.Unlock()
	return m, nil
}

// Changes is every release Check last found between this copy and the newest, newest first, the newest included.
func (u *Updater) Changes() []Manifest {
	u.mu.Lock()
	defer u.mu.Unlock()
	return slices.Clone(u.changes)
}

// Changes is each verified release on channel after current, up to and including newest, newest first: what an
// update to newest brings. A source that can't list its releases, or a list that fails, gives newest alone.
func Changes(ctx context.Context, src Source, keys []ed25519.PublicKey, channel, current string, newest Manifest) []Manifest {
	out := []Manifest{newest}
	h, ok := src.(History)
	if !ok {
		return out
	}
	rels, err := h.Releases(ctx, channel)
	if err != nil {
		return out
	}
	seen := map[string]bool{newest.Version: true}
	for _, rel := range rels {
		m, err := rel.Verify(keys)
		if err != nil || seen[m.Version] || !Newer(m.Version, current) || Newer(m.Version, newest.Version) {
			continue
		}
		seen[m.Version] = true
		out = append(out, m)
	}
	newestFirst(out)
	return out
}

// History is every verified release on channel the source lists, newest first: this copy's own and those before it
// as well as any newer, for reading their notes.
func (u *Updater) History(ctx context.Context, channel string) ([]Manifest, error) {
	h, ok := u.Source.(History)
	if !ok {
		return nil, errors.New("the release source can't list its releases")
	}
	rels, err := h.Releases(ctx, channel)
	if err != nil {
		return nil, err
	}
	var out []Manifest
	seen := map[string]bool{}
	for _, rel := range rels {
		m, err := rel.Verify(u.Keys)
		if err != nil || seen[m.Version] {
			continue
		}
		seen[m.Version] = true
		out = append(out, m)
	}
	newestFirst(out)
	return out, nil
}

// newestFirst sorts ms by version, newest first.
func newestFirst(ms []Manifest) {
	slices.SortStableFunc(ms, func(a, b Manifest) int {
		switch {
		case Newer(a.Version, b.Version):
			return -1
		case Newer(b.Version, a.Version):
			return 1
		}
		return 0
	})
}

// Latest is the release Check last kept.
func (u *Updater) Latest() (Manifest, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.m, u.rel != nil
}

func (u *Updater) latest() (Release, Manifest, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.rel == nil {
		return Release{}, Manifest{}, ErrNoRelease
	}
	return *u.rel, u.m, nil
}

func (u *Updater) cache() Cache {
	return Cache{Dir: u.CacheDir, Source: u.Source, Progress: u.Progress}
}

// artifact is this copy's artifact of the latest release.
func (u *Updater) artifact() (Manifest, Artifact, error) {
	_, m, err := u.latest()
	if err != nil {
		return Manifest{}, Artifact{}, err
	}
	kind, platform := u.Kind, u.Platform
	if kind == "" {
		kind = KindBinary
	}
	if platform == "" {
		platform = Platform()
	}
	a, ok := m.Artifact(platform, kind)
	if !ok {
		return Manifest{}, Artifact{}, fmt.Errorf("%s has nothing for %s", m.Version, platform)
	}
	return m, a, nil
}

// Download fetches and checks this copy's artifact of the latest release ahead of Install, which then reads it
// from the cache.
func (u *Updater) Download(ctx context.Context) error {
	m, a, err := u.artifact()
	if err != nil {
		return err
	}
	r, err := u.cache().Open(ctx, m, a)
	if err != nil {
		return err
	}
	return r.Close()
}

// Install downloads (unless Download did), stages and swaps in this copy's artifact of the latest release. Oriel
// runs it from the next start, which Boot confirms or rolls back.
func (u *Updater) Install(ctx context.Context) error {
	if u.Installer == nil {
		return errors.New("this copy of Oriel can't replace itself")
	}
	m, a, err := u.artifact()
	if err != nil {
		return err
	}
	if v, ok := u.Ready(); ok && v == m.Version {
		return nil
	}
	r, err := u.cache().Open(ctx, m, a)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()
	if err := u.Installer.stage(ctx, m, a, r, u.Progress); err != nil {
		return err
	}
	u.Progress.report(Step{Stage: StageSwap, Detail: u.Installer.Target})
	return u.Installer.Swap()
}

// Ready is the version swapped in and waiting for a restart.
func (u *Updater) Ready() (string, bool) {
	if u.Installer == nil {
		return "", false
	}
	p, ok := u.Installer.Pending()
	if !ok || !Newer(p.Version, u.Version) {
		return "", false
	}
	return p.Version, true
}
