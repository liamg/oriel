package shell_test

import (
	"image"
	"image/draw"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// railAt is the middle of the narrow sidebar's nth entry from the top, under its » (one project or a directory).
func railAt(n int) render.Point { return render.Point{X: 22, Y: kit.TitleH + 30 + 17 + float32(n)*34} }

// The rail against 01-ide's strip: the mockup's entries, those not registered yet dimmed.
func TestRailSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Frame()
	s.Frame()
	s.Golden("rail")
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../../testdata/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	strip := image.Rect(0, kit.TitleH, kit.RailW, int(apptest.MockupSize.Y)-kit.StatusH)
	want, got := image.NewRGBA(strip), image.NewRGBA(strip)
	draw.Draw(want, strip, mock, strip.Min, draw.Src)
	draw.Draw(got, strip, img, strip.Min, draw.Src)
	d, _ := snapshot.Compare(want, got, 0.1)
	t.Logf("rail against 01-ide: %s", d)
	if d.Mean > 0.05 {
		t.Errorf("the rail is %s from 01-ide's", d)
	}
}

// laterEntry is a project's panel whose view isn't registered.
var laterEntry = uireg.RailEntry{Name: "later", Icon: "git", Title: "Later", Command: "view.toggle", Args: kit.ViewArgs("later", "Later", 0),
	Scope: uireg.ScopeProjectEntry, Order: 35}

// An entry lights once its view is registered, and toggles the view in the left dock.
func TestRailTogglesAView(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.Host().Contribute(laterEntry)
	s.Frame()
	later := apptest.Centre(s.Node("Later").Bounds)
	s.Click(later, 0)
	if len(s.Host().Layout().Visible()) != 0 {
		t.Fatal("an unavailable entry opened something")
	}
	s.Registry().View("later", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("the later view") }))
	s.Frame()
	s.Click(later, 0)
	if v := s.Host().Layout().Visible(); len(v) != 1 || v[0].ID != "later" {
		t.Fatalf("after a click the layout shows %v", v)
	}
	if w := s.Host().Layout().Find(s.Host().Layout().Visible()[0]); w == nil || w.Edge != layout.Left {
		t.Errorf("the later view isn't in the left dock: %+v", w)
	}
	s.Click(later, 0)
	if v := s.Host().Layout().Visible(); len(v) != 0 {
		t.Errorf("a second click left %v showing", v)
	}
}

// A click on an entry beside a page puts the page away, then does what the entry does; a panel already showing
// stays, rather than toggling off under it.
func TestRailClosesThePage(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	pinged := 0
	s.Host().Command(registry.Command{ID: "test.ping", Run: func(registry.Ctx, registry.Args) error { pinged++; return nil }})
	s.Host().Contribute(uireg.RailEntry{Name: "ping", Icon: "◆", Title: "Ping", Command: "test.ping", Order: 36})
	s.Host().Contribute(laterEntry)
	s.Registry().View("later", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("the later view") }))
	s.Frame()
	stats := func() {
		t.Helper()
		s.Run("stats.open", nil)
		s.Frame()
		s.Frame()
		if !s.Shown("stats") {
			t.Fatal("no stats page")
		}
	}
	stats()
	s.Click(apptest.Centre(s.Node("Ping").Bounds), 0)
	s.Frame()
	if s.Shown("stats") || pinged != 1 {
		t.Errorf("after a click on Ping, the stats page is up: %v, pinged %d times", s.Shown("stats"), pinged)
	}
	later := apptest.Centre(s.Node("Later").Bounds)
	s.Click(later, 0)
	stats()
	s.Click(later, 0)
	s.Frame()
	if s.Shown("stats") {
		t.Error("a click on Later left the stats page up")
	}
	if v := s.Host().Layout().Visible(); len(v) != 1 || v[0].ID != "later" {
		t.Errorf("the click beside the page toggled its panel: the layout shows %v", v)
	}
}

// A plugin's entry, from Go or Lua, appears in order, badges, and goes when disposed.
func TestRailPluginEntries(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	s.WriteConfig(map[string]string{"init.lua": `oriel.rail.add{ name = "tickets", icon = "◆", title = "Tickets", command = "Settings", order = 70 }`})
	s.ReloadLua()
	d := s.Host().Contribute(uireg.RailEntry{Name: "inbox", Icon: "requests", Title: "Inbox", Command: "Settings", Order: 80,
		Badge: func() int { return 7 }})
	s.Frame()
	s.Frame()
	var names []string
	for _, e := range uireg.RailEntries(s.Registry()) {
		names = append(names, e.Name)
	}
	if got, want := strings.Join(names, " "), "repos reviews explorer stats search usage git jobs pull-request requests review plan-docs tests debug ship tickets inbox assistant settings"; got != want {
		t.Errorf("rail = %q, want %q", got, want)
	}
	if !strings.Contains("\n"+s.Text()+"\n", "\n7\n") {
		t.Error("the inbox's badge isn't drawn")
	}
	d.Dispose()
	s.Frame()
	if strings.Contains("\n"+s.Text()+"\n", "\n7\n") {
		t.Error("the badge outlived its entry")
	}
}

// The key of an entry that isn't available yet says so, rather than failing.
func TestRailKeyUnavailable(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: t.TempDir(), Setup: func(h plugin.Host) {
		h.Contribute(laterEntry)
		h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-L>", Command: "project.panel", Args: registry.Args{"name": "later"}, Desc: "Later"})
	}})
	r.WaitText("ORIEL")
	r.TypeKeys("<D-L>")
	r.WaitText("Later isn't available yet")
	for _, n := range r.Store.State().Notifications {
		if n.Severity == core.SevError {
			t.Errorf("an error: %s", n.Text)
		}
	}
}

// Requests' number counts the requests that want you, the agent's answer last, not those it's working on or owes.
func TestRailRequestsCountsWhatWantsYou(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	you := core.Comment{ID: "c1", Author: core.AuthorYou, Body: "fix it", Delivered: true}
	working := core.Comment{ID: "c2", Author: core.AuthorAgent, Body: "on it", Progress: true}
	answer := core.Comment{ID: "c2", Author: core.AuthorAgent, Body: "Which one?"}
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Agent: core.AgentState{Provider: "fake"},
		Threads: []core.Thread{
			{ID: "busy", Status: core.ThreadOpen, Busy: true, Comments: []core.Comment{you, working}},
			{ID: "owed", Status: core.ThreadOpen, Comments: []core.Comment{you}},
			{ID: "asks", Status: core.ThreadOpen, Comments: []core.Comment{you, answer}},
			{ID: "done", Status: core.ThreadResolved, Comments: []core.Comment{you, answer}},
		}}})
	s.Store().Apply(core.SessionFocused{ID: "w"})
	s.Frame()
	for _, c := range s.Registry().Contributions("rail.entry") {
		if e := c.(uireg.RailEntry); e.Name == "requests" {
			if n := e.Badge(); n != 1 {
				t.Errorf("Requests' badge is %d, not 1: the request that asks you", n)
			}
			return
		}
	}
	t.Fatal("no Requests entry")
}
