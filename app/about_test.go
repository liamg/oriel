package app

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

// cursorShown says the title bar's logo draws its cursor: a fill in logo.cursor inside the logo.
func (s *errScene) cursorShown(t *testing.T) bool {
	t.Helper()
	b := s.within("Sessions", "Oriel").Bounds
	want := s.ui.Theme().C(tLogoCursor)
	for _, op := range s.list.Ops {
		if op.Kind == render.OpPath && op.Style.Fill == want && op.Path != nil && len(op.Path.Segs) > 0 {
			if p := op.Path.Segs[0].Pts[0]; p.X >= b.Min.X && p.X <= b.Max.X && p.Y >= b.Min.Y && p.Y <= b.Max.Y {
				return true
			}
		}
	}
	return false
}

// The logo's cursor blinks while the pointer is on it, and holds still with reduced motion; off it, it stays lit.
func TestLogoBlinksUnderThePointer(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1600, Y: 500})
	o := s.ui.Options()
	o.ReducedMotion = false
	s.ui.SetOptions(o)
	s.now = time.Unix(1000, 0)
	s.frame()
	if !s.cursorShown(t) {
		t.Fatal("the logo has no cursor")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(s.within("Sessions", "Oriel").Bounds)})
	s.frame()
	var seen []bool
	for range 4 {
		s.frame()
		seen = append(seen, s.cursorShown(t))
		s.now = s.now.Add(logoBlink)
	}
	if !seen[0] || seen[1] || !seen[2] || seen[3] {
		t.Errorf("under the pointer the cursor went %v, want on, off, on, off", seen)
	}
	o.ReducedMotion = true
	s.ui.SetOptions(o)
	for range 3 {
		s.now = s.now.Add(logoBlink)
		s.frame()
		if !s.cursorShown(t) {
			t.Fatal("with reduced motion the cursor blinked")
		}
	}
}

// A click on the logo opens the About card under it: the version and how updates stand, Copy report, Help and
// Website. Escape and its ✕ put it away, opened alone and over Settings.
func TestLogoOpensTheAboutCard(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	x := s.about()
	for _, over := range []string{"", "Settings"} {
		for _, how := range []string{"Esc", "✕"} {
			if over == "Settings" {
				s.run("Settings", nil)
			}
			s.frame()
			s.click(centre(s.within("Sessions", "Oriel").Bounds))
			s.frame()
			s.frame()
			if text := s.frameText(); !containsAll(text, "ORIEL", "Updates are off in this build", "Copy report", "Help", "Website") {
				t.Fatalf("over %q, the About card:\n%s", over, text)
			}
			if how == "Esc" {
				s.typeKeys("Esc")
			} else {
				s.click(centre(s.within("About Oriel", "Close").Bounds))
			}
			s.frame()
			if x.open || strings.Contains(s.frameText(), "Copy report") {
				t.Errorf("over %q, %s didn't put the About card away", over, how)
				x.open = false
			}
			if over == "Settings" {
				s.settings().open = false
			}
		}
	}
	s.lint("the title bar")
}

func (s *errScene) about() *aboutPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*aboutPlugin); ok {
			return x
		}
	}
	return nil
}
