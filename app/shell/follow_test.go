package shell_test

import (
	"context"
	"sync/atomic"
	"testing"

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
}
