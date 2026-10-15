package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/liamg/oriel/wire"
)

// Updater is the app's side of updating: it finds releases, installs this copy of Oriel, and pushes each release to
// the workers. It keeps no UI; the app and `oriel update` drive it.
type Updater struct {
	Source  Source
	Keys    []ed25519.PublicKey
	Version string // the running version
	// Installer replaces this copy; nil when it can't be (a build run from a checkout): workers are still updated.
	Installer *Installer
	Kind      string // this copy's artifact kind: KindBundle for the macOS .app, KindBinary if ""
	Platform  string // Platform() if ""
	CacheDir  string // downloads, kept for this machine and each worker

	pub  Publisher
	once sync.Once
	mu   sync.Mutex
	rel  *Release
	m    Manifest
}

// ErrNoRelease is asking to install before a release was found.
var ErrNoRelease = errors.New("no update has been found")

// Check asks the source for channel's newest release and keeps it if it verifies and is newer than this copy.
func (u *Updater) Check(ctx context.Context, channel string) (Manifest, error) {
	rel, m, err := Check(ctx, u.Source, u.Keys, channel, u.Version)
	if err != nil {
		return Manifest{}, err
	}
	u.mu.Lock()
	u.rel, u.m = &rel, m
	u.mu.Unlock()
	return m, nil
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

func (u *Updater) cache() Cache { return Cache{Dir: u.CacheDir, Source: u.Source} }

// Install downloads, stages and swaps in this copy's artifact of the latest release. Oriel runs it from the next
// start, which Boot confirms or rolls back.
func (u *Updater) Install(ctx context.Context) error {
	if u.Installer == nil {
		return errors.New("this copy of Oriel can't replace itself")
	}
	_, m, err := u.latest()
	if err != nil {
		return err
	}
	if v, ok := u.Ready(); ok && v == m.Version {
		return nil
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
		return fmt.Errorf("%s has nothing for %s", m.Version, platform)
	}
	r, err := u.cache().Open(ctx, m, a)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := u.Installer.Stage(ctx, m, a, r); err != nil {
		return err
	}
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

// Serve lets conn's worker fetch the releases offered to it.
func (u *Updater) Serve(conn wire.Conn) { u.publisher().Serve(conn) }

func (u *Updater) publisher() *Publisher {
	u.once.Do(func() { u.pub.Keys = u.Keys })
	return &u.pub
}

// Pushed is what PushTo did.
type Pushed struct {
	From, To string // the worker's version before, and the one it restarts onto; "" when it was left alone
}

// PushTo updates conn's worker to the latest release when it's older: it fetches, verifies and runs the release,
// then swaps and restarts onto it. A worker that doesn't take updates (this machine's, which follows the app's own
// binary) is left alone.
func (u *Updater) PushTo(ctx context.Context, conn wire.Conn) (Pushed, error) {
	rel, m, err := u.latest()
	if err != nil {
		return Pushed{}, err
	}
	st, err := StatusOf(ctx, conn)
	if err != nil {
		var re *wire.RemoteError
		if errors.As(err, &re) && strings.HasSuffix(re.Message, wire.ErrNoMethod.Error()) {
			return Pushed{}, nil
		}
		return Pushed{}, err
	}
	if !Newer(m.Version, st.Version) {
		return Pushed{}, nil
	}
	if st.RolledBack != nil && st.RolledBack.Version == m.Version {
		return Pushed{}, fmt.Errorf("it rolled %s back: %s", m.Version, st.RolledBack.Reason)
	}
	if _, err := u.publisher().Push(ctx, conn, rel, u.cache().Opener(m)); err != nil {
		return Pushed{}, err
	}
	if err := Swap(ctx, conn); err != nil {
		return Pushed{}, err
	}
	return Pushed{From: st.Version, To: m.Version}, nil
}
