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

// ClickTop clicks the run of text reading text painted last, on top of any other, and draws a frame.
func (s *Scene) ClickTop(text string) {
	s.T.Helper()
	s.drawn(text)
	p, ok := s.TextAt(text)
	if !ok {
		s.T.Fatalf("no %q in the frame", text)
	}
	s.Click(p, 0)
	s.Frame()
}

// ClickNode clicks the control named label (ClickOn), and draws two frames after for what it opened to lay out.
func (s *Scene) ClickNode(label string) {
	s.T.Helper()
	s.ClickOn(label)
	s.Frame()
	s.Frame()
}

// ClickOn clicks the middle of the control named label once it's enabled and settled, in the same place two frames
// running, pressing and letting go on the frame it was found in.
func (s *Scene) ClickOn(label string) {
	s.T.Helper()
	s.Quiet()
	var last *render.AccessNode
	for deadline := time.Now().Add(Timeout); ; {
		s.Frame()
		n := s.Find(label)
		if n != nil && last != nil && !n.Disabled && n.Bounds == last.Bounds {
			s.Click(Centre(n.Bounds), 0)
			return
		}
		last = n
		if time.Now().After(deadline) {
			s.T.Fatalf("%q never enabled and settled:\n%s", label, s.Text())
		}
		s.RunQueued()
		time.Sleep(5 * time.Millisecond)
	}
}

// RightClick right-clicks p, and answers the frame's text after: the menu it opened.
func (s *Scene) RightClick(p render.Point) string {
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonSecondary})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonSecondary})
	s.Frame()
	s.Frame()
	return s.Text()
}
