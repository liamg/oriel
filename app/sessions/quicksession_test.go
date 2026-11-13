package sessions_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// startedAs is what a session was started on, as the launcher's choices set it.
func startedAs(se core.SessionState) string {
	a := se.Agent
	return fmt.Sprintf("kind=%s agent=%s model=%s effort=%s sandboxed=%v network=%s backend=%s repos=%d prompt=%q",
		se.Kind, a.Provider, a.Model, a.Effort, a.Sandboxed, a.Network, a.Backend, len(se.Projects), se.Prompt)
}

// A quick session (⇧⌘N, the start view's Quick Session, or Quick session on the + menu) is what New session's Start
// starts when nothing in the launcher is changed: a work session on the default agent and model, sandboxed as
// sandbox.default says, with no repos and no prompt.
func TestQuickSessionIsTheLaunchersDefaults(t *testing.T) {
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.Do(func() error { // the test host has no sandbox factory
		return r.Host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})
	newest := func(what string, n int) core.SessionState {
		t.Helper()
		var se core.SessionState
		r.UntilState(what, func(st core.State) bool {
			if len(st.Sessions) != n {
				return false
			}
			f, ok := st.Session(st.Focus)
			if !ok {
				return false
			}
			se = f.Snapshot()
			return true
		})
		return se
	}

	r.TypeKeys("<D-n>")
	r.Latest("the launcher", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
	r.Latest("the default agent picked", func(f headless.Frame) bool { return !strings.Contains(f.Text(), "checking which agents") })
	r.TypeKeys("<D-CR>")
	lw.Fake.WaitStarted(t)
	launched := newest("the launcher's session", 1)

	r.TypeKeys("<D-N>")
	lw.Fake.WaitStarted(t)
	quick := newest("the quick session", 2)
	if got, want := startedAs(quick), startedAs(launched); got != want {
		t.Errorf("the quick session started as\n  %s\nwant what the launcher's Start does unchanged:\n  %s", got, want)
	}
	if quick.Kind != core.KindWork || quick.Agent.Provider == "" {
		t.Errorf("the quick session isn't a work session on an agent: %s", startedAs(quick))
	}
}

// Quick Session is on the start view, under New Session, and on the + button's menu.
func TestQuickSessionIsOffered(t *testing.T) {
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitText("Quick Session")
	r.Do(func() error { // the test host has no sandbox factory
		return r.Host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})
	r.Run("session.start", registry.Args{"kind": "work", "name": "first", "provider": "fake", "repos": []string{lw.Repo}})
	lw.Fake.WaitStarted(t)
	r.WaitAccess("the + after the tab", func(w *apptest.Window) bool { return w.Access("New session") != nil })
	r.RightClickAccess("New session")
	r.WaitText("Quick session")
	r.WaitText("New session…")
	r.ClickTop("Quick session")
	lw.Fake.WaitStarted(t)
	r.UntilState("the quick session from the menu", func(st core.State) bool { return len(st.Sessions) == 2 })
}
