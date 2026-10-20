package app

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/update"
)

// updateRig is a signed release in a directory, and an installed copy of Oriel in another.
type updateRig struct {
	t        *testing.T
	key      ed25519.PrivateKey
	trust    []ed25519.PublicKey
	src      string
	self     *update.Installer
	relaunch atomic.Int32
}

func newUpdateRig(t *testing.T) *updateRig {
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &updateRig{t: t, key: key, trust: []ed25519.PublicKey{key.Public().(ed25519.PublicKey)}, src: t.TempDir()}
	root := t.TempDir()
	r.self = &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"), Probe: noProbe}
	if err := os.WriteFile(r.self.Target, []byte("oriel 1.0.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	return r
}

func noProbe(context.Context, string, string) error { return nil }

// publish puts version out on the stable channel, with notes.
func (r *updateRig) publish(version, notes string) []byte {
	r.t.Helper()
	body := []byte("oriel " + version)
	a := update.Describe("oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"), update.Platform(), update.KindBinary, body)
	rel, err := update.Sign(r.key, update.Manifest{Version: version, Channel: update.Stable, Notes: notes, Artifacts: []update.Artifact{a}})
	if err != nil {
		r.t.Fatal(err)
	}
	if err := update.Write(r.src, rel, map[string][]byte{a.Name: body}); err != nil {
		r.t.Fatal(err)
	}
	return body
}

func (r *updateRig) config(cfg *Config) {
	cfg.Updates = &Updates{
		Updater: &update.Updater{Source: update.Dir(r.src), Keys: r.trust, Version: "1.0.0", Installer: r.self,
			CacheDir: r.t.TempDir()},
		Relaunch: func() error { r.relaunch.Add(1); return nil },
	}
}

func (s *errScene) updates() *updatePlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*updatePlugin); ok {
			return x
		}
	}
	return nil
}

// PL-6, the app's side: :UpdateCheck opens the card with the release's notes; Update runs the update on the
// Updating page, each step a line, and restarts onto it.
func TestUpdateCardUpdatesAndRestarts(t *testing.T) {
	r := newUpdateRig(t)
	s := newSceneWith(t, "", "", mockupSize, r.config)
	r.publish("2.0.0", "## Highlights\n\n- Faster session start\n- **Rollback** on a failed health check")

	s.run("UpdateCheck", nil)
	s.frame()
	s.until(t, "the card with the release", func() bool { return strings.Contains(s.frameText(), "Oriel 2.0.0") })
	s.wantFrame(t, "WHAT'S NEW", "you have 1.0.0 · stable channel", "Highlights", "Faster session start", "Rollback", "Later", "Update")
	s.lint("the update card")

	s.click(centre(s.within("Oriel 2.0.0", "Update").Bounds))
	s.until(t, "the restart", func() bool { return s.sh.relaunch.Load() != nil })
	if b, _ := os.ReadFile(r.self.Target); string(b) != "oriel 2.0.0" {
		t.Fatalf("this copy is %q", b)
	}
	s.until(t, "the Updating page, step by step", func() bool {
		return containsAll(s.frameText(), "UPDATING ORIEL", "1.0.0  →  2.0.0", "Found Oriel 2.0.0 on the stable channel: you have 1.0.0",
			"Downloading oriel-", "Verified oriel-", "Extracting oriel-", "Checking the new version starts: oriel version says 2.0.0",
			"Swapping in the new oriel; the old one is kept for a rollback", "Oriel 2.0.0 is installed", "Restarting into Oriel 2.0.0")
	})
	s.lint("the Updating page")
	var err error
	s.sh.relaunchAfter(&err)
	if err != nil || r.relaunch.Load() != 1 {
		t.Fatalf("closing the window after the restart relaunched %d times: %v", r.relaunch.Load(), err)
	}
}

// Automatic, the default: a start checks before anything else, and a newer release is installed on the Updating page
// and restarted onto.
func TestAutomaticUpdateAtStart(t *testing.T) {
	r := newUpdateRig(t)
	r.publish("2.0.0", "")
	s := newSceneWith(t, "", "", mockupSize, r.config)
	s.until(t, "the restart onto 2.0.0", func() bool { return s.sh.relaunch.Load() != nil })
	if b, _ := os.ReadFile(r.self.Target); string(b) != "oriel 2.0.0" {
		t.Fatalf("this copy is %q", b)
	}
	s.until(t, "the Updating page", func() bool {
		return containsAll(s.frameText(), "UPDATING ORIEL", "Oriel 2.0.0 is installed", "Restarting into Oriel 2.0.0")
	})
}

// A release that comes out while Oriel runs is downloaded first, quietly. Ask then offers it; automatic installs it
// for the next restart, and says so, without restarting by itself.
func TestUpdateDownloadsBeforeItSays(t *testing.T) {
	for _, mode := range []string{updateAsk, updateAutomatic} {
		t.Run(mode, func(t *testing.T) {
			r := newUpdateRig(t)
			s := newSceneWith(t, "", "", mockupSize, r.config)
			s.reg.Options().Set("updates.mode", registry.LayerUser, registry.At{}, mode)
			r.publish("1.1.0", "")
			s.updates().check(false)
			cached := filepath.Join(s.updates().updater().CacheDir, "1.1.0", "oriel-"+strings.ReplaceAll(update.Platform(), "/", "-"))
			if _, err := os.Stat(cached); err != nil {
				t.Fatalf("not downloaded first: %v", err)
			}
			switch mode {
			case updateAsk:
				if !hasNotice(s.store.State(), "update.available") {
					t.Fatal("no offer")
				}
				if _, ok := s.updates().ready(); ok {
					t.Fatal("ask mode installed by itself")
				}
			case updateAutomatic:
				s.until(t, "the install", func() bool { _, ok := s.updates().ready(); return ok })
				s.until(t, "the notice", func() bool { return hasNotice(s.store.State(), "update.ready") })
				s.frame()
				s.until(t, "the status line", func() bool { return strings.Contains(s.frameText(), "update ready · restart") })
				if s.sh.relaunch.Load() != nil {
					t.Fatal("restarted mid-work by itself")
				}
			}
		})
	}

	r := newUpdateRig(t)
	s := newSceneWith(t, "", "", mockupSize, r.config)
	r.publish("1.0.0", "")
	s.updates().check(false)
	if hasNotice(s.store.State(), "update.available") || hasNotice(s.store.State(), "update.ready") {
		t.Fatal("offered the version already running")
	}
}

// The first start of a newer version says what it updated from, once.
func TestUpdatedSaysSo(t *testing.T) {
	r := newUpdateRig(t)
	state := t.TempDir()
	os.WriteFile(filepath.Join(state, "version"), []byte("0.9.0\n"), 0o644)
	cfg := func(c *Config) { r.config(c); c.StateDir = state }
	s := newSceneWith(t, "", "", mockupSize, cfg)
	if !hasNotice(s.store.State(), "update.done") {
		t.Fatal("no notice of the update")
	}
	if b, _ := os.ReadFile(filepath.Join(state, "version")); strings.TrimSpace(string(b)) != "1.0.0" {
		t.Errorf("the version kept is %q", b)
	}
	s2 := newSceneWith(t, "", "", mockupSize, cfg)
	if hasNotice(s2.store.State(), "update.done") {
		t.Error("said so again")
	}
}

// Settings → updates: the mode and the channel.
func TestUpdateOptions(t *testing.T) {
	s := newSceneWith(t, "", "", mockupSize, nil)
	for name, want := range map[string]string{"updates.mode": "automatic", "updates.channel": "stable"} {
		if v, _, err := s.reg.Options().Get(name, registry.At{}); err != nil || v != want {
			t.Errorf("%s = %v, %v; want %s", name, v, err, want)
		}
	}
	if s.reg.Available("update.install", nil) {
		t.Error("a build without updates offers update.install")
	}
}

func hasNotice(st core.State, id string) bool {
	for _, n := range st.Notifications {
		if n.ID == id {
			return true
		}
	}
	return false
}

// Config.Started runs once the window is open and every plugin is active: the updater's health check after a
// swap waits for it.
func TestStartedRunsOnceTheWindowIsUp(t *testing.T) {
	started := make(chan struct{}, 2)
	startAppWith(t, t.TempDir(), "", nil, func(cfg *Config) { cfg.Started = func() { started <- struct{}{} } })
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("Started wasn't called")
	}
	select {
	case <-started:
		t.Fatal("Started was called twice")
	case <-time.After(100 * time.Millisecond):
	}
}
