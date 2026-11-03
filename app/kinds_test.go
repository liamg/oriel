package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	appsessions "github.com/liamg/oriel/app/sessions"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// A kind from Lua is a kind like the built-ins: the launcher offers it after them, the host starts it, its prepare
// fills in the spec first, and its label, chips and capabilities are what the app asks of it.
func TestLuaSessionKind(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = time.Now()
	s.Lua(`L.session.kind("incident", { title = "Incident", launcher = true, order = 70, can = { "ships" },
		label = function(se) return "incident · " .. se.name end,
		chips = function(se) return { "P1", { text = "pager", help = "agent/sessions" } } end,
		prepare = function(spec) return { title = "pager: " .. spec.prompt } end })`)
	var got []string
	for _, k := range appsessions.LauncherKinds(registry.Kinds(s.Registry()), "") {
		got = append(got, string(k.Kind))
	}
	if strings.Join(got, " ") != "work plan review incident" {
		t.Errorf("the launcher's kinds = %v", got)
	}
	if !registry.Can(s.Registry(), "incident", core.Ships) || registry.Can(s.Registry(), "incident", core.AddProjects) {
		t.Error("the kind's capabilities")
	}
	done := make(chan error, 1)
	var id core.SessionID
	go func() {
		var err error
		ss, _ := registry.SessionsOf(s.Registry())
		id, err = ss.Start(context.Background(), registry.Args{"kind": "incident", "prompt": "api down", "repos": []string{lw.Repo},
			"background": true})
		done <- err
	}()
	s.Until("the incident session", func() bool {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			return true
		default:
			return false
		}
	})
	sess, ok := s.Store().Session(id)
	if !ok {
		t.Fatal("no session")
	}
	se := sess.Snapshot()
	if se.Kind != "incident" || se.Name != "pager: api down" || len(se.Projects) != 1 {
		t.Errorf("session = %+v", se)
	}
	if l := registry.Label(s.Registry(), se); l != "incident · pager: api down" {
		t.Errorf("label = %q", l)
	}
	if c := registry.Chips(s.Registry(), se); len(c) != 2 || c[0].Text != "P1" || c[1].Text != "pager" || c[1].Help != "agent/sessions" {
		t.Errorf("chips = %+v", c)
	}
}

// A direct session can't take more projects or be deleted, and a review's isn't its own to ship: what the window
// offers follows its kind's capabilities.
func TestCapabilitiesGateCommands(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	for kind, want := range map[core.Kind]bool{core.KindWork: true, core.KindDirect: false, core.KindReview: true} {
		se := core.SessionState{ID: core.SessionID("s-" + kind), Kind: kind, Status: core.StatusQuiet}
		if err := s.Store().Apply(core.SessionSynced{Session: se}); err != nil {
			t.Fatal(err)
		}
		if err := s.Store().Apply(core.SessionFocused{ID: se.ID}); err != nil {
			t.Fatal(err)
		}
		s.Frame()
		if got := s.Registry().Available("project.add", nil); got != want {
			t.Errorf("project.add on a %s session: %v", kind, got)
		}
		if got := s.Registry().Available("review.ai.fix", nil); got != (kind != core.KindReview) {
			t.Errorf("review.ai.fix on a %s session: %v", kind, got)
		}
	}
}

// oriel.session.done says when a session looks finished: why, or nil for not.
func TestLuaSessionDone(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Lua(`L.session.done("incident", function(se) if se.name == "pager" then return "The page is resolved." end end)`)
	if why := registry.Done(s.Registry(), core.SessionState{Name: "pager"}); why != "The page is resolved." {
		t.Errorf("done = %q", why)
	}
	if why := registry.Done(s.Registry(), core.SessionState{Name: "other"}); why != "" {
		t.Errorf("another session's done = %q", why)
	}
}

// oriel.session.keep keeps a value for a session, which oriel.session.kept reads back after the Lua's reloaded;
// nil forgets it.
func TestLuaSessionKeep(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Lua(`L.session.keep("s1", "myplugin.view", { tab = "notes", n = 2 })`)
	s.ReloadLua()
	if out := s.Lua(`L.session.kept("s1", "myplugin.view").tab .. L.session.kept("s1", "myplugin.view").n`); len(out) != 1 || out[0] != "notes2" {
		t.Errorf("kept = %v", out)
	}
	if out := s.Lua(`L.session.kept("s2", "myplugin.view")`); len(out) != 1 || out[0] != nil {
		t.Errorf("another session's = %v", out)
	}
	s.Lua(`L.session.keep("s1", "myplugin.view", nil)`)
	if out := s.Lua(`L.session.kept("s1", "myplugin.view")`); len(out) != 1 || out[0] != nil {
		t.Errorf("forgotten = %v", out)
	}
}
