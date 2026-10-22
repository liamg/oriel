package settings_test

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
)

// cursorShown says the title bar's logo draws its cursor: a fill in logo.cursor inside the logo.
func cursorShown(s *apptest.Scene) bool {
	b := s.Within("Sessions", "Oriel").Bounds
	want := s.UI().Theme().C(kit.TLogoCursor)
	for _, op := range s.List().Ops {
		if op.Kind == render.OpPath && op.Style.Fill == want && op.Path != nil && len(op.Path.Segs) > 0 {
			if p := op.Path.Segs[0].Pts[0]; p.X >= b.Min.X && p.X <= b.Max.X && p.Y >= b.Min.Y && p.Y <= b.Max.Y {
				return true
			}
		}
	}
	return false
}

// The logo's cursor blinks while the pointer is on it, and holds still unfocused and with reduced motion; off it, it stays lit.
func TestLogoBlinksUnderThePointer(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 500}})
	o := s.UI().Options()
	o.ReducedMotion = false
	s.UI().SetOptions(o)
	s.Now = time.Unix(1000, 0)
	s.Frame()
	if !cursorShown(s) {
		t.Fatal("the logo has no cursor")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.Within("Sessions", "Oriel").Bounds)})
	s.Frame()
	var seen []bool
	for range 4 {
		s.Frame()
		seen = append(seen, cursorShown(s))
		s.Now = s.Now.Add(settings.LogoBlink)
	}
	if !seen[0] || seen[1] || !seen[2] || seen[3] {
		t.Errorf("under the pointer the cursor went %v, want on, off, on, off", seen)
	}
	s.UI().SetWindow(false, true)
	for range 3 {
		s.Now = s.Now.Add(settings.LogoBlink)
		s.Frame()
		if !cursorShown(s) {
			t.Fatal("with the window behind another app the cursor blinked")
		}
	}
	s.UI().SetWindow(true, true)
	o.ReducedMotion = true
	s.UI().SetOptions(o)
	for range 3 {
		s.Now = s.Now.Add(settings.LogoBlink)
		s.Frame()
		if !cursorShown(s) {
			t.Fatal("with reduced motion the cursor blinked")
		}
	}
}

// A click on the logo opens the About card under it: the version and how updates stand, Copy report, Help and
// Website. Escape and its ✕ put it away.
func TestLogoOpensTheAboutCard(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	x := apptest.PluginOf[*settings.About](s, "about")
	for _, how := range []string{"Esc", "✕"} {
		s.Frame()
		s.Click(apptest.Centre(s.Within("Sessions", "Oriel").Bounds), 0)
		s.Frame()
		s.Frame()
		if !s.Has("ORIEL", "Updates are off in this build", "Copy report", "Help", "Website") {
			t.Fatalf("the About card:\n%s", s.Text())
		}
		if how == "Esc" {
			s.Type("Esc")
		} else {
			s.Click(apptest.Centre(s.Within("About Oriel", "Close").Bounds), 0)
		}
		s.Frame()
		if x.Open() || strings.Contains(s.Text(), "Copy report") {
			t.Fatalf("%s didn't put the About card away", how)
		}
	}
	s.Lint("the title bar")
}
