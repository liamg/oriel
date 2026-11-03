package shell_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// counted counts its activations and deactivations.
type counted struct {
	activated, deactivated atomic.Int32
}

func (p *counted) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test/counted"} }

func (p *counted) Activate(context.Context, plugin.Host) error {
	p.activated.Add(1)
	return nil
}

func (p *counted) Deactivate(context.Context) error {
	p.deactivated.Add(1)
	return nil
}

// Plugins live as long as the app: bringing a session in another directory to the front moves the window onto it
// underneath them, and doesn't start them again.
func TestFollowingASessionKeepsThePluginsRunning(t *testing.T) {
	lw := rig.NewHost(t)
	p := &counted{}
	w := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W), Plugins: []plugin.Plugin{p}})
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	start := func(name string) core.SessionID {
		t.Helper()
		if err := w.Do(func() error {
			return w.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": name, "provider": "fake", "repos": lw.Repo})
		}); err != nil {
			t.Fatal(err)
		}
		lw.Fake.WaitStarted(t)
		st := w.UntilState("session "+name, func(st core.State) bool {
			se, ok := st.Session(core.SessionID(name))
			return ok && kit.SessionDir(se.Snapshot()) != ""
		})
		se, _ := st.Session(core.SessionID(name))
		return se.ID()
	}
	dirOf := func(id core.SessionID) string {
		se, _ := w.Store.Session(id)
		return kit.SessionDir(se.Snapshot())
	}
	follows := func(id core.SessionID) {
		t.Helper()
		if err := w.Store.Apply(core.SessionFocused{ID: id}); err != nil {
			t.Fatal(err)
		}
		want := dirOf(id)
		w.WaitFor("the window on "+string(id)+"'s directory", func() bool {
			win, ok := kit.WindowOf(w.Host)
			return ok && win.Dir() == want
		})
	}
	a, b := start("a"), start("b")
	if dirOf(a) == dirOf(b) {
		t.Fatalf("both sessions are in %s", dirOf(a))
	}
	follows(a)
	follows(b)
	follows(a)
	if n, d := p.activated.Load(), p.deactivated.Load(); n != 1 || d != 0 {
		t.Errorf("following sessions activated the plugin %d times and deactivated it %d times, want once and never", n, d)
	}
	// Moving is quick: no DirChanged hook goes over its budget, which would be a warning.
	w.Do(func() error {
		for _, hk := range w.Host.Hooks() {
			if hk.Event == "DirChanged" {
				t.Logf("%-28s %-58s p99 %v", hk.Source, hk.Label, hk.P99)
			}
		}
		return nil
	})
	for _, n := range w.Store.State().Notifications {
		if n.Severity == core.SevWarning || n.Severity == core.SevError {
			t.Errorf("following sessions raised a %s from %s: %s: %s", n.Severity, n.Source, n.Title, n.Text)
		}
	}
}

// holding takes Window.Hold as the window moves, and lets the test release it.
type holding struct {
	release chan func()
}

func (p *holding) Manifest() plugin.Manifest        { return plugin.Manifest{ID: "test/holding"} }
func (p *holding) Deactivate(context.Context) error { return nil }
func (p *holding) Activate(_ context.Context, h plugin.Host) error {
	kit.OnDirChanged(h, "holds the window", func(kit.DirChanged) {
		w, _ := kit.WindowOf(h)
		p.release <- w.Hold()
	})
	return nil
}

// A plugin still reading what the new directory's views need holds the window behind the opening cover, and the
// directory is laid out once it lets go.
func TestAMoveWaitsForWhatHoldsIt(t *testing.T) {
	p := &holding{release: make(chan func(), 1)}
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: t.TempDir(), Plugins: []plugin.Plugin{p}})
	dir := t.TempDir()
	if err := r.Do(func() error {
		w, _ := kit.WindowOf(r.Host)
		return w.Reopen(dir, "")
	}); err != nil {
		t.Fatal(err)
	}
	release := <-p.release
	r.WaitText("Opening")
	time.Sleep(300 * time.Millisecond)
	r.Do(func() error { r.Host.Redraw(); return nil })
	if fs := r.Win.Frames(); !strings.Contains(fs[len(fs)-1].Text(), "Opening") {
		t.Fatal("the window laid the directory out while a plugin held it")
	}
	release()
	r.WaitGone("Opening")
}
