package app

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
)

func (s *errScene) showing(id string) bool {
	for _, v := range s.sh.layout.root.Visible() {
		if v.ID == id {
			return true
		}
	}
	return false
}

// textPoint is the middle of the first run of text in the frame that reads text.
func (s *errScene) textPoint(t *testing.T, text string) render.Point {
	t.Helper()
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	t.Fatalf("no %q in the frame", text)
	return render.Point{}
}

func TestLogPanelCloses(t *testing.T) {
	s := newErrScene(t, "")
	s.run("Log", nil)
	s.frame()
	s.frame()
	if !s.showing("log") {
		t.Fatal(":Log didn't open the log")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: s.textPoint(t, "Log")})
	s.frame()
	s.frame()
	s.click(centre(s.access("Close Log").Bounds))
	if s.showing("log") {
		t.Error("the tab's ✕ didn't close the log")
	}

	s.run("Log", nil)
	s.frame()
	k, _ := s.reg.Lookup([]string{ViewKeyMode, "global"}, keymap.Parse("<Esc>"))
	if k == nil {
		t.Fatal("Escape does nothing in a focused view")
	}
	s.run(k.Command, k.Args)
	if s.showing("log") {
		t.Error("Escape didn't close the docked log")
	}

	s.run("view.open", registry.Args{"id": "log"})
	s.run(k.Command, k.Args)
	if !s.showing("log") {
		t.Error("Escape closed a view in the main area")
	}
}

func TestSearchClosesOnTheNextFrame(t *testing.T) {
	s := newErrScene(t, "")
	s.now = time.Unix(1000, 0)
	s.run("picker.open", registry.Args{"source": "grep"})
	s.frame()
	s.frame()
	s.typeKeys("Esc")
	if s.picker().open {
		t.Fatal("Escape didn't close the search")
	}
	if next := s.ui.NextFrame(); next.IsZero() || next.After(s.now) {
		t.Errorf("the frame that closed the search, still showing it, asks for the next at %v, not now", next)
	}
	s.frame()
	if s.picker().open || s.ui.NextFrame().After(s.now) {
		t.Error("the search is still up a frame later")
	}
}

func TestEventsViewCloses(t *testing.T) {
	s := newErrScene(t, "")
	s.run("omnibox.open", registry.Args{"query": "Events"})
	s.frame()
	s.typeKeys("CR")
	s.frame()
	if !s.showing("events") {
		t.Fatal("running Events from the omnibox didn't open the event stream")
	}
	s.keyCommand(t, "<Esc>")
	if s.showing("events") {
		t.Errorf("Escape didn't close the event stream (keys went to %q)", s.ui.KeyMode())
	}
}
