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
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// updateRig is a signed release in a directory, an installed copy of Oriel in another, and remote workers that take
// updates as `oriel worker --connect` does.
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

// remoteWorker is a worker on another machine at version, taking updates through worker.Options.Handlers.
type remoteWorker struct {
	in        *update.Installer
	client    worker.Client
	restarted atomic.Int32
}

func (r *updateRig) worker(id core.WorkerID, version string) *remoteWorker {
	r.t.Helper()
	root := r.t.TempDir()
	rw := &remoteWorker{in: &update.Installer{Target: filepath.Join(root, "oriel"), Dir: filepath.Join(root, "updates"), Probe: noProbe}}
	if err := os.WriteFile(rw.in.Target, []byte("oriel "+version), 0o755); err != nil {
		r.t.Fatal(err)
	}
	rcv := &update.Receiver{Installer: rw.in, Keys: r.trust, Version: version, Restart: func() { rw.restarted.Add(1) }}
	lw := newWorkerWith(r.t, worker.Options{ID: id, Name: string(id), Version: version, Handlers: []func(wire.Conn){rcv.Serve}})
	a, b := wire.Pipe()
	lw.w.Serve(b)
	r.t.Cleanup(func() { a.Close(); b.Close() })
	rw.client = worker.Client{Conn: a}
	return rw
}

func (rw *remoteWorker) updatedTo() string {
	p, _ := rw.in.Pending()
	return p.Version
}

func (s *errScene) updates() *updateExt {
	for _, e := range s.exts {
		if x, ok := e.(*updateExt); ok {
			return x
		}
	}
	return nil
}

// PL-6, the app's side: :UpdateCheck opens the card with the release's notes; Update sends the release to the worker
// that's behind and installs this copy; the status line says "update ready · restart", which quits and relaunches.
// A worker that connects later, still behind, is updated as it connects.
func TestUpdateCardInstallsAndRestarts(t *testing.T) {
	r := newUpdateRig(t)
	r.publish("2.0.0", "## Highlights\n\n- Workers update over the wire\n- **Rollback** on a failed health check")
	devbox := r.worker("devbox", "1.0.0")
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		r.config(cfg)
		cfg.Workers = append(cfg.Workers, devbox.client)
	})
	waitState(t, s.store, "devbox", func(st core.State) bool { return len(st.Workers) == 1 })

	s.run("UpdateCheck", nil)
	s.frame()
	s.until(t, "the card with the release", func() bool { return strings.Contains(s.frameText(), "Oriel 2.0.0") })
	s.wantFrame(t, "WHAT'S NEW", "you have 1.0.0 · stable channel", "Highlights", "Workers update over the wire", "Rollback", "Later", "Update")
	s.lint("the update card")

	s.click(centre(s.within("Oriel 2.0.0", "Update").Bounds))
	s.until(t, "the update installed", func() bool { _, ok := s.updates().ready(); return ok })
	if b, _ := os.ReadFile(r.self.Target); string(b) != "oriel 2.0.0" {
		t.Fatalf("this copy is %q", b)
	}
	s.until(t, "devbox swapped and restarting", func() bool { return devbox.updatedTo() == "2.0.0" && devbox.restarted.Load() == 1 })
	s.until(t, "Restart on the card", func() bool { return strings.Contains(s.frameText(), "Oriel 2.0.0 is installed") })
	s.wantFrame(t, "update ready · restart")
	s.lint("an update ready")
	if !hasNotice(s.store.State(), "update.ready") {
		t.Error("no notice says the update is ready")
	}

	// A worker that connects later, still on 1.0.0, is updated before it's used.
	late := r.worker("laptop", "1.0.0")
	s.sh.whenSessions(func(x *sessions) { x.serve(x.ctx, x.h, late.client, "") })
	s.until(t, "laptop updated", func() bool { return late.updatedTo() == "2.0.0" && late.restarted.Load() == 1 })

	s.run("update.notes", nil)
	s.frame()
	s.click(centre(s.within("Oriel 2.0.0", "Later").Bounds))
	s.frame()
	s.click(centre(s.access("Update ready · restart").Bounds))
	s.frame()
	var err error
	s.sh.relaunchAfter(&err)
	if err != nil || r.relaunch.Load() != 1 {
		t.Fatalf("closing the window after the restart relaunched %d times: %v", r.relaunch.Load(), err)
	}
}

// In ask mode the check on the timer offers a newer release in a notice; on the current version it says nothing.
func TestUpdateCheckOffersARelease(t *testing.T) {
	was := updateFirstCheck
	updateFirstCheck = 10 * time.Millisecond
	t.Cleanup(func() { updateFirstCheck = was })
	r := newUpdateRig(t)
	r.publish("1.1.0", "")
	s := newSceneWith(t, "", "", mockupSize, r.config)
	s.frame()
	s.until(t, "the offer", func() bool { return hasNotice(s.store.State(), "update.available") })
	s.run("update.notes", nil)
	s.until(t, "the card", func() bool { return strings.Contains(s.frameText(), "Oriel 1.1.0") })
	s.lint("the card from the offer")
	if _, ok := s.updates().ready(); ok {
		t.Fatal("ask mode installed by itself")
	}

	r2 := newUpdateRig(t)
	r2.publish("1.0.0", "")
	s2 := newSceneWith(t, "", "", mockupSize, r2.config)
	s2.updates().check(false)
	if hasNotice(s2.store.State(), "update.available") {
		t.Fatal("offered the version already running")
	}
}

// Settings → updates: the mode and the channel.
func TestUpdateOptions(t *testing.T) {
	s := newSceneWith(t, "", "", mockupSize, nil)
	for name, want := range map[string]string{"updates.mode": "ask", "updates.channel": "stable"} {
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

// Config.Started runs once the window is open and every extension is active: the updater's health check after a
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
