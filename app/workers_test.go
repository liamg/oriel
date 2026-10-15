package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

func (s *errScene) workers() *workersExt {
	for _, e := range s.exts {
		if x, ok := e.(*workersExt); ok {
			return x
		}
	}
	return nil
}

// workersScene is the app with this machine's worker, its keys file and certificate in temporary directories.
func workersScene(t *testing.T, change ...func(*Config)) (*errScene, *localWorker, *workersExt) {
	t.Helper()
	lw := newLocalWorker(t)
	state := t.TempDir()
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		lw.connect(t)(cfg)
		cfg.StateDir = state
		for _, f := range change {
			f(cfg)
		}
	})
	waitState(t, s.store, "this machine's worker", func(st core.State) bool { return len(st.Workers) == 1 })
	return s, lw, s.workers()
}

// paired is what Add a worker says to run on the other machine.
type paired struct{ addr, fingerprint, key, name string }

func parsePairing(t *testing.T, cmd string) paired {
	t.Helper()
	f := strings.Fields(cmd)
	if len(f) != 10 || f[0] != "oriel" || f[1] != "worker" {
		t.Fatalf("the command is %q", cmd)
	}
	var p paired
	for i := 2; i+1 < len(f); i += 2 {
		switch f[i] {
		case "--connect":
			p.addr = f[i+1]
		case "--fingerprint":
			p.fingerprint = f[i+1]
		case "--key":
			p.key = f[i+1]
		case "--name":
			p.name = f[i+1]
		default:
			t.Fatalf("the command has %s: %q", f[i], cmd)
		}
	}
	return p
}

// remote is a worker on another machine, dialling the app as `oriel worker --connect` does.
type remote struct {
	lw   *localWorker
	p    paired
	stop context.CancelFunc
	done chan error
}

func dialApp(t *testing.T, p paired, id core.WorkerID) *remote {
	t.Helper()
	r := &remote{lw: newWorkerWith(t, worker.Options{ID: id, Name: string(id)}), p: p}
	r.connect(t)
	return r
}

func (r *remote) connect(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r.stop, r.done = cancel, make(chan error, 1)
	h := wire.Hello{Name: r.p.name, Key: r.p.key, OS: runtime.GOOS, Arch: runtime.GOARCH}
	go func() { r.done <- r.lw.w.ServeApp(ctx, r.p.addr, r.p.fingerprint, h) }()
}

// ended is how the worker's connection ended, the app's loop running meanwhile.
func (r *remote) ended(t *testing.T, s *errScene) error {
	t.Helper()
	var err error
	s.until(t, "the worker's connection to end", func() bool {
		select {
		case err = <-r.done:
			return true
		default:
			return false
		}
	})
	return err
}

func workerIn(st core.State, id core.WorkerID) (core.WorkerState, bool) {
	i := slices.IndexFunc(st.Workers, func(w core.WorkerState) bool { return w.ID == id })
	if i < 0 {
		return core.WorkerState{}, false
	}
	return st.Workers[i], true
}

// AI-15 done when: "A real worker (worker.Worker running ServeApp) over TLS to the app's listener on 127.0.0.1:0,
// with the key from Add a worker, shows online on the page and in the store, and its sessions arrive; the dialog
// says it connected." Then it goes away and comes back, is revoked, and forgotten.
func TestAddARemoteWorker(t *testing.T) {
	s, _, x := workersScene(t)
	s.run("workers.open", nil)
	s.frame()
	s.frame()
	s.wantFrame(t, "WORKERS", "1 online", "not listening for workers elsewhere")
	s.lint("the Workers page")

	s.keyCommand(t, "a")
	if x.dlg == nil {
		t.Fatal("a didn't open Add a worker")
	}
	s.frame()
	s.typeInto(t, "Name", "devbox")
	s.typeInto(t, "Listen on", "127.0.0.1:0")
	s.lint("Add a worker")
	s.keyCommand(t, "<D-CR>")
	if x.dlg == nil || x.dlg.added == nil {
		t.Fatalf("Add didn't pair: %+v", x.dlg)
	}
	if v, _, _ := s.reg.Options().Get("workers.listen", registry.At{}); v != "127.0.0.1:0" {
		t.Errorf("workers.listen is %v", v)
	}
	s.frame()
	s.click(centre(s.access("Copy").Bounds))
	s.frame()
	p := parsePairing(t, s.clip.text)
	if p.name != "devbox" || strings.HasSuffix(p.addr, ":0") || !strings.HasPrefix(p.addr, "127.0.0.1:") {
		t.Fatalf("pairing %+v", p)
	}
	s.wantFrame(t, "RUN THIS ON DEVBOX", wire.Words(p.fingerprint), "Waiting for devbox to connect", "Copied")
	s.lint("the pairing")

	// The key is kept only as its hash, owner-only; the certificate too.
	keys := filepath.Join(s.sh.cfg.ConfigDir, workersFile)
	b, err := os.ReadFile(keys)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), p.key) || !strings.Contains(string(b), wire.HashKey(p.key)) {
		t.Errorf("the keys file:\n%s", b)
	}
	for _, f := range []string{keys, filepath.Join(s.sh.cfg.StateDir, "workers", "tls.key"), filepath.Join(s.sh.cfg.StateDir, "workers", "tls.crt")} {
		if fi, err := os.Stat(f); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v %v", f, fi.Mode(), err)
		}
	}

	r := dialApp(t, p, "devbox")
	s.until(t, "devbox online", func() bool { w, ok := workerIn(s.store.State(), "devbox"); return ok && w.Connected })
	s.until(t, "the dialog to say so", func() bool { return strings.Contains(s.frameText(), "devbox connected") })
	if _, err := r.lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindScratch, Name: "notes"}); err != nil {
		t.Fatal(err)
	}
	waitState(t, s.store, "the remote session", func(st core.State) bool {
		se, ok := st.Session("notes")
		return ok && se.Snapshot().Worker == "devbox"
	})
	s.click(centre(s.within("Add a worker", "Done").Bounds))
	s.frame()
	if x.dlg != nil {
		t.Fatal("Done didn't close the dialog")
	}
	s.wantFrame(t, "2 online", "listening on 127.0.0.1:")
	s.click(centre(s.within("Workers list", "devbox").Bounds))
	s.frame()
	s.wantFrame(t, "SESSIONS", "1 session", "notes", "dials Oriel with its key")
	s.lint("a remote worker")

	// It goes away, and comes back.
	r.stop()
	if err := r.ended(t, s); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	s.until(t, "devbox offline", func() bool { w, _ := workerIn(s.store.State(), "devbox"); return !w.Connected })
	s.until(t, "the page to say so", func() bool { return strings.Contains(s.frameText(), "offline since") })
	if _, ok := s.store.State().Session("notes"); ok {
		t.Error("an offline worker's session stayed")
	}
	r.connect(t)
	s.until(t, "devbox back", func() bool { w, _ := workerIn(s.store.State(), "devbox"); return w.Connected })

	// Revoke asks once, cuts it off, and it's refused after.
	s.click(centre(s.access("Revoke").Bounds))
	s.frame()
	s.click(centre(s.access("Sure?").Bounds))
	s.frame()
	if err := r.ended(t, s); err == nil {
		t.Fatal("the revoked worker's connection ended cleanly")
	}
	s.until(t, "devbox revoked", func() bool { return strings.Contains(s.frameText(), "revoked") })
	r.connect(t)
	var rejected *wire.ErrRejected
	if err := r.ended(t, s); !errors.As(err, &rejected) {
		t.Fatalf("a revoked key got %v", err)
	}

	// Forget takes it off the list.
	s.click(centre(s.access("Forget").Bounds))
	s.frame()
	s.click(centre(s.access("Sure?").Bounds))
	s.frame()
	if _, ok := workerIn(s.store.State(), "devbox"); ok {
		t.Error("Forget left devbox in the store")
	}
	if b, _ := os.ReadFile(keys); strings.Contains(string(b), "devbox") {
		t.Errorf("Forget left devbox in the keys file:\n%s", b)
	}
	s.wantFrame(t, "1 online")
	if strings.Contains(s.frameText(), "devbox") {
		t.Errorf("devbox is still listed:\n%s", s.frameText())
	}
}

// AI-15: a wrong key is refused, a wrong fingerprint is refused by the worker, and a worker claiming another's
// name with its key is cut off.
func TestRemoteWorkersRefused(t *testing.T) {
	s, _, x := workersScene(t)
	if err := s.host.Run(context.Background(), "workers.add", registry.Args{"name": "local"}); err == nil {
		t.Error(`a worker was added as "local"`)
	}
	s.run("workers.add", registry.Args{"name": "devbox", "listen": "127.0.0.1:0"})
	p := parsePairing(t, x.dlg.added.command)
	var rejected *wire.ErrRejected

	bad := p
	bad.key = strings.Repeat("0", 64)
	if err := dialApp(t, bad, "devbox").ended(t, s); !errors.As(err, &rejected) || rejected.Welcome.Status != wire.StatusDenied {
		t.Fatalf("a wrong key got %v", err)
	}
	s.run("workers.open", registry.Args{"name": "devbox"})
	s.frame()
	s.until(t, "the refusal", func() bool { return strings.Contains(s.frameText(), "wrong key") })

	pin := p
	pin.fingerprint = wire.FingerprintDER([]byte("someone else"))
	if err := dialApp(t, pin, "devbox").ended(t, s); err == nil || errors.As(err, &rejected) || !strings.Contains(err.Error(), "fingerprint") {
		t.Fatalf("a wrong fingerprint got %v", err)
	}

	if err := dialApp(t, p, "laptop").ended(t, s); err == nil || errors.As(err, &rejected) {
		t.Fatalf("a worker claiming devbox's name got %v", err)
	}
	if _, ok := workerIn(s.store.State(), "laptop"); ok {
		t.Error("the worker that claimed devbox's name joined")
	}
	if w, ok := workerIn(s.store.State(), "devbox"); ok && w.Connected {
		t.Error("devbox is online")
	}
}

// AI-15: Rename is the app's name for a worker, everywhere it's named, and kept in the keys file.
func TestRenameAWorker(t *testing.T) {
	s, _, x := workersScene(t)
	s.run("workers.open", nil)
	s.frame()
	s.keyCommand(t, "r")
	if x.dlg == nil || x.dlg.rename != core.LocalWorker {
		t.Fatalf("r opened %+v", x.dlg)
	}
	s.frame()
	for _, r := range "Studio" {
		s.ui.Key(render.KeyEvent{Key: string(r)})
		s.frame()
	}
	s.lint("Rename")
	s.keyCommand(t, "<D-CR>")
	if x.dlg != nil {
		t.Fatalf("Save didn't close the dialog: %+v", x.dlg)
	}
	if got := s.store.State().WorkerName(core.LocalWorker); got != "Studio" {
		t.Fatalf("the worker reads %q", got)
	}
	s.frame()
	s.wantFrame(t, "Studio")
	if b, _ := os.ReadFile(filepath.Join(s.sh.cfg.ConfigDir, workersFile)); !strings.Contains(string(b), `"label": "Studio"`) {
		t.Errorf("the keys file:\n%s", b)
	}
	s.run("workers.rename", registry.Args{"name": "Studio", "to": ""})
	if w, _ := workerIn(s.store.State(), core.LocalWorker); w.Label != "" {
		t.Errorf("renaming to nothing left %q", w.Label)
	}
}

// AI-15: Restart drains this machine's worker the way a rebuild does, and the app reconnects to the new one.
func TestRestartThisMachinesWorker(t *testing.T) {
	started := make(chan struct{}, 1)
	var home string
	s, lw, _ := workersScene(t, func(cfg *Config) {
		cfg.LocalWorker = func(context.Context) (worker.Client, error) {
			nw := newWorkerWith(t, worker.Options{Home: home})
			started <- struct{}{}
			a, b := wire.Pipe()
			nw.w.Serve(b)
			t.Cleanup(func() { a.Close(); b.Close() })
			return worker.Client{Conn: a}, nil
		}
	})
	home = lw.w.Info().StateDir
	s.run("workers.open", nil)
	s.frame()
	s.frame()
	s.click(centre(s.access("Restart").Bounds))
	s.until(t, "the new worker", func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	s.until(t, "the drain to end", func() bool { return s.sh.draining.Load() == nil })
	waitState(t, s.store, "this machine's worker back", func(st core.State) bool {
		w, ok := workerIn(st, core.LocalWorker)
		return ok && w.Connected
	})
}

// ui.md 45 / 01h: the sidebar's Workers with its figure, and the omnibox's Pages.
func TestWorkersEntryPoints(t *testing.T) {
	s, _, x := workersScene(t)
	if got := x.figure(); got != "1 online" {
		t.Errorf("the figure is %q", got)
	}
	items := pagesSource(s.host).Items(nil, "")
	i := slices.IndexFunc(items, func(it registry.PickerItem) bool { return it.Label == "Workers" })
	if i < 0 || items[i].Detail != "1 online" {
		t.Fatalf("Pages: %+v", items)
	}
	if err := pagesSource(s.host).Run(nil, items[i]); err != nil || !x.open {
		t.Fatalf("the Pages entry: %v, open %v", err, x.open)
	}
	s.frame()
	s.run("Stats", nil)
	s.frame()
	s.frame()
	if x.open {
		t.Error("Stats left the Workers page up")
	}
	s.run("Workers", registry.Args{"args": "local"})
	if !x.open || x.sel != core.LocalWorker {
		t.Errorf(":Workers local: open %v, sel %q", x.open, x.sel)
	}
}

// The page with three workers: this machine's, a remote one online with a signed-out login, and one offline.
func TestWorkersPageSnapshot(t *testing.T) {
	s := newSceneWith(t, "", "", mockupSize, nil)
	x := s.workers()
	gb := uint64(1 << 30)
	now := s.now
	for _, ev := range []core.Event{
		core.WorkerChanged{Worker: core.WorkerState{ID: core.LocalWorker, Name: "mbp", Host: "mbp.local", OS: "darwin", Arch: "arm64", Version: "v0.4.2",
			Build: "3f9c1a2be0d4", Providers: []string{"claude", "codex"}, Connected: true, Since: now.Add(-3 * time.Hour), Cores: 10, CPU: .23,
			Mem: 12 * gb, MemTotal: 32 * gb, Sampled: now}},
		core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Name: "devbox", Host: "devbox.lan", OS: "linux", Arch: "amd64", Version: "v0.4.2",
			Build: "77ab01cd9e", Providers: []string{"claude", "codex"}, Connected: true, Since: now.Add(-2 * time.Hour), Cores: 32, CPU: .61,
			Mem: 40 * gb, MemTotal: 64 * gb, Sampled: now.Add(-4 * time.Second),
			SignedOut: []core.Login{{Provider: "codex", Worker: "devbox", Detail: "token expired"}}}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Account: "acct", AccountName: "ada@example.com", Plan: "Claude Max", Tier: "Max 20×",
			Workers: []core.WorkerID{core.LocalWorker, "devbox"}}},
		core.SessionSynced{Session: core.SessionState{ID: "inf-2231-fix-stale-usage-prices", Kind: core.KindWork, Worker: "devbox",
			Agent: core.AgentState{Provider: "claude", Running: true, Working: true}}},
		core.SessionSynced{Session: core.SessionState{ID: "rounding", Name: "Round half-even", Kind: core.KindWork, Worker: "devbox",
			Agent: core.AgentState{Provider: "claude", Running: true}}},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	x.mu.Lock()
	x.keys = []workerKey{
		{Name: "devbox", Hash: wire.HashKey("k1"), Added: now.Add(-72 * time.Hour), Seen: now.Add(-2 * time.Hour), Addr: "10.0.0.5:51234"},
		{Name: "ci-runner", Hash: wire.HashKey("k2"), Added: now.Add(-240 * time.Hour), Seen: now.Add(-26 * time.Hour), Addr: "10.0.0.9:40022"},
	}
	x.mu.Unlock()
	s.run("workers.open", registry.Args{"name": "devbox"})
	s.golden("workers")
	s.lint("the Workers page")
	s.wantFrame(t, "WORKERS", "2 online", "1 offline", "mbp", "this machine", "devbox", "ci-runner", "offline since", "signed out: token expired",
		"ada@example.com", "2 sessions", "Round half-even", "last from 10.0.0.5:51234")
}

// ui.md 01e: with nothing open there's no sidebar, so the start view's links reach the Workers page too.
func TestWorkersFromTheStartView(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	s.frame()
	s.wantFrame(t, "Stats", "Usage", "Workers")
	s.click(centre(s.access("Workers").Bounds))
	s.frame()
	x := s.workers()
	if x == nil || !x.open {
		t.Fatal("the start view's Workers link didn't open the page")
	}
}
