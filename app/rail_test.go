package app

import (
	"image"
	"image/draw"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// railAt is the middle of the rail's nth entry from the top.
func railAt(n int) render.Point { return render.Point{X: 22, Y: titleH + 8 + 16 + float32(n)*36} }

// SH-16: the rail against 01-ide's strip: the mockup's entries, those not registered yet dimmed.
func TestRailSnapshot(t *testing.T) {
	s := newErrScene(t, "")
	s.frame()
	s.frame()
	s.golden("rail")
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../_design/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	strip := image.Rect(0, titleH, railW, int(mockupSize.Y)-statusH)
	want, got := image.NewRGBA(strip), image.NewRGBA(strip)
	draw.Draw(want, strip, mock, strip.Min, draw.Src)
	draw.Draw(got, strip, img, strip.Min, draw.Src)
	d, _ := snapshot.Compare(want, got, 0.1)
	t.Logf("rail against 01-ide: %s", d)
	if d.Mean > 0.05 {
		t.Errorf("the rail is %s from 01-ide's", d)
	}
}

// SH-16: an entry lights once its view is registered, and toggles the view in the left dock.
func TestRailTogglesAView(t *testing.T) {
	s := newErrScene(t, "")
	s.frame()
	explorer := railAt(0)
	s.click(explorer)
	if len(s.sh.layout.root.Visible()) != 0 {
		t.Fatal("an unavailable entry opened something")
	}
	s.reg.View("explorer", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.TextNode("the explorer") }))
	s.frame()
	s.click(explorer)
	if v := s.sh.layout.root.Visible(); len(v) != 1 || v[0].ID != "explorer" {
		t.Fatalf("after a click the layout shows %v", v)
	}
	if w := s.sh.layout.root.Find(s.sh.layout.root.Visible()[0]); w == nil || w.Edge != layout.Left {
		t.Errorf("the explorer isn't in the left dock: %+v", w)
	}
	s.click(explorer)
	if v := s.sh.layout.root.Visible(); len(v) != 0 {
		t.Errorf("a second click left %v showing", v)
	}
}

// SH-16: a plugin's entry, from Go or Lua, appears in order, badges, and goes when disposed.
func TestRailPluginEntries(t *testing.T) {
	s := newErrScene(t, "")
	s.writeConfig(t, map[string]string{"init.lua": `oriel.rail.add{ name = "tickets", icon = "◆", title = "Tickets", command = "Settings", order = 70 }`})
	s.lua().reload(false)
	d := s.host.Contribute(registry.RailEntry{Name: "inbox", Icon: "requests", Title: "Inbox", Command: "Settings", Order: 80,
		Badge: func() int { return 7 }})
	s.frame()
	s.frame()
	var names []string
	for _, e := range registry.RailEntries(s.reg) {
		names = append(names, e.Name)
	}
	if got, want := strings.Join(names, " "), "explorer search git requests debug tests tickets inbox settings"; got != want {
		t.Errorf("rail = %q, want %q", got, want)
	}
	if !strings.Contains("\n"+s.frameText()+"\n", "\n7\n") {
		t.Error("the inbox's badge isn't drawn")
	}
	d.Dispose()
	s.frame()
	if strings.Contains("\n"+s.frameText()+"\n", "\n7\n") {
		t.Error("the badge outlived its entry")
	}
}

// SH-16: the key of an entry that isn't available yet says so, rather than failing.
func TestRailKeyUnavailable(t *testing.T) {
	r := startApp(t, t.TempDir(), t.TempDir(), nil)
	r.waitText("ORIEL")
	r.typeKeys("<D-E>")
	r.waitText("Explorer isn't available yet")
	for _, n := range r.store.State().Notifications {
		if n.Severity == core.SevError {
			t.Errorf("an error: %s", n.Text)
		}
	}
}
