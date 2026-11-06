package apptest

import (
	"time"

	"github.com/liamg/oriel/render"
)

// ClickText clicks the middle of the first run of text that reads text, once it's drawn in the same place two
// frames running and what's under it isn't disabled: what a person would click.
func (s *Scene) ClickText(text string) {
	s.T.Helper()
	s.Quiet()
	s.drawn(text)
	var last render.Point
	for deadline, seen := time.Now().Add(Timeout), false; ; {
		s.Frame()
		p, ok := s.textPoint(text)
		if ok && seen && p == last && !disabledAt(s.Access(), p) {
			s.Click(p, 0)
			return
		}
		last, seen = p, ok
		if time.Now().After(deadline) {
			s.T.Fatalf("%q never settled on screen, enabled:\n%s", text, s.Text())
		}
		s.RunQueued()
		time.Sleep(5 * time.Millisecond)
	}
}

// textPoint is the middle of the first run of text that reads text, if one is drawn.
func (s *Scene) textPoint(text string) (render.Point, bool) {
	if s.List() == nil {
		return render.Point{}, false
	}
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}, true
		}
	}
	return render.Point{}, false
}
