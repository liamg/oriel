package shell_test

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// railShown says whether the rail is in the window: its toolbar is labelled Views.
func railShown(s *apptest.Scene) bool { return s.Find("Views") != nil }

// Each start-screen button opens what it opens on Home, which has no slot for the rail.
func TestHomeHasNoRail(t *testing.T) {
	scene := func(t *testing.T) *apptest.Scene {
		w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: rig.NopExec{}, Load: rig.SteadyLoad})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { w.Close() })
		s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.Host, cfg.Store = w, w.Store() }})
		s.Frame()
		return s
	}
	entries := uireg.StartEntries(scene(t).Registry())
	if len(entries) < 10 {
		t.Fatalf("only %d start entries", len(entries))
	}
	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			s := scene(t)
			if e.Unlisted {
				s.Run(e.Command, e.Args)
			} else {
				s.Click(apptest.Centre(s.StartCard(e.Title).Bounds), 0)
			}
			for range 4 {
				s.Frame()
			}
			if railShown(s) {
				t.Errorf("%s opened from the start view shows the rail", e.Title)
			}
		})
	}
}

// A window on a directory is a workspace: it has the rail and the status line.
func TestWorkspaceHasRail(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Frame()
	s.Frame()
	if !railShown(s) {
		t.Error("no rail on a directory")
	}
	s.Run("settings.open", nil)
	s.Frame()
	s.Frame()
	if !railShown(s) {
		t.Error("a page over the workspace took the rail away")
	}
}

// The window is Home until a session is running, a workspace while one is, and Home again once the last one ends.
func TestFrameFollowsSessions(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Frame()
	if railShown(s) || s.Find("Start") == nil {
		t.Fatal("the window doesn't start on Home")
	}
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork}})
	s.Store().Apply(core.SessionFocused{ID: "w"})
	s.Frame()
	s.Frame()
	if !railShown(s) {
		t.Error("a running session has no rail")
	}
	s.Store().Apply(core.SessionArchived{ID: "w", Archived: true})
	s.Frame()
	s.Frame()
	if railShown(s) {
		t.Error("the rail outlived the last session")
	}
	if s.Find("Start") == nil {
		t.Error("the window isn't back on Home")
	}
}

// A frame is a contribution: a plugin's frame whose When holds comes before the built-ins.
func TestFrameContribution(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Registry().View("test.bare", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("bare frame") }))
	d := s.Registry().Contribute(uireg.Frame{ID: "test.bare", Order: -1, When: func(uireg.Window) bool { return true },
		Regions: []uireg.Region{{View: "test.bare", Area: uireg.AreaMain}}})
	s.Frame()
	if !strings.Contains(s.Text(), "bare frame") || s.Find("Start") != nil {
		t.Errorf("the plugin's frame isn't the window:\n%s", s.Text())
	}
	d.Dispose()
	s.Frame()
	if s.Find("Start") == nil {
		t.Error("Home didn't come back when the frame went")
	}
}

// Home has the status line's global segments (the update chip, the error counts, any segment marked
// uireg.GlobalView) and none of a session's; a workspace has both.
func TestHomeShowsGlobalStatus(t *testing.T) {
	r := rig.NewReleases(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Releasing(r)})
	s.Registry().View(shell.StatusLeft+"98-bound", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("SESSION ONLY") }))
	s.Registry().View(shell.StatusLeft+"99-global", uireg.GlobalView{View: uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("EVERYWHERE") })})
	s.Registry().Options().Set("updates.mode", registry.LayerUser, registry.At{}, "automatic")
	r.Publish("1.1.0", "")
	s.CheckUpdates()
	s.Until("the install", func() bool { return s.Registry().Available("update.restart", nil) })
	s.Host().Notify(core.Notice{ID: "test.broke", Severity: core.SevError, Text: "it broke"})
	s.Frame()
	s.Frame()
	if railShown(s) {
		t.Fatal("Home has the rail")
	}
	text := s.Text()
	for _, want := range []string{"update ready · restart", "EVERYWHERE"} {
		if !strings.Contains(text, want) {
			t.Errorf("Home's status line lacks %q:\n%s", want, text)
		}
	}
	if s.Find("1 errors") == nil {
		t.Errorf("Home's status line has no error count:\n%s", text)
	}
	if strings.Contains(text, "SESSION ONLY") {
		t.Error("a session's segment shows on Home")
	}
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork}})
	s.Store().Apply(core.SessionFocused{ID: "w"})
	s.Frame()
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "SESSION ONLY") || !strings.Contains(text, "EVERYWHERE") {
		t.Errorf("the workspace's status line:\n%s", text)
	}
}

// :Log and the inbox, docked views in a workspace, open on Home as pages, and close with the page's Close or Escape.
func TestDockedViewsArePagesOnHome(t *testing.T) {
	for _, tt := range []struct{ command, id, page, close string }{
		{"Log", "log", "Log", "click"}, {"inbox.open", "inbox", "Inbox", "click"},
		{"Log", "log", "Log", "Escape"}, {"inbox.open", "inbox", "Inbox", "Escape"},
	} {
		t.Run(tt.id+" "+tt.close, func(t *testing.T) {
			s := apptest.New(t, apptest.Options{})
			s.Frame()
			s.Run(tt.command, nil)
			s.Frame()
			s.Frame()
			if s.Find(tt.page+" page") == nil || !s.Showing(tt.id) {
				t.Fatalf("%s didn't open as a page on Home:\n%s", tt.command, s.Text())
			}
			if railShown(s) {
				t.Error("the page brought the rail")
			}
			if tt.close == "Escape" {
				s.Press("<Esc>") // as the window takes it: the view's keymaps, then what has focus
			} else {
				s.ClickTop("Close")
			}
			s.Frame()
			s.Frame()
			if s.Find(tt.page+" page") != nil || s.Showing(tt.id) {
				t.Errorf("%s left %s open", tt.close, tt.id)
			}
			if s.Find("Start") == nil {
				t.Error("the start view didn't come back")
			}
		})
	}
}
