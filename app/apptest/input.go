package apptest

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// Apply applies events to the state, as the session host's would.
func (s *Scene) Apply(evs ...core.Event) {
	s.T.Helper()
	for _, ev := range evs {
		if err := s.Store().Apply(ev); err != nil {
			s.T.Fatal(err)
		}
	}
}

// Run runs a command and waits until it has finished, drawing frames while its slow part is off the loop.
func (s *Scene) Run(id string, args registry.Args) {
	s.T.Helper()
	var err error
	done := false
	s.Registry().Run(registry.OnDone(context.Background(), func(e error) { err, done = e, true }), id, args)
	if !done && !s.Headless.Until(Timeout, func() bool { return done }) {
		s.T.Fatalf("%s never finished", id)
	}
	if err != nil {
		s.T.Fatal(err)
	}
}

// Press presses key as the window does a key on its own (Headless.Press).
func (s *Scene) Press(key string) {
	s.T.Helper()
	if err := s.Headless.Press(key); err != nil {
		s.T.Fatal(err)
	}
}

// Type presses each key (a character, or a name such as "CR", "Esc") on what has focus.
func (s *Scene) Type(keys ...string) {
	s.T.Helper()
	for _, k := range keys {
		if !s.Key(k) {
			s.T.Fatalf("nothing focused takes %q", k)
		}
	}
}

// Hold presses mods (0 lets go) and draws, d later on the clock.
func (s *Scene) Hold(m render.Mods, d time.Duration) {
	s.UI().Mods(m)
	s.Frame()
	s.Now = s.Now.Add(d)
	s.Frame()
	s.Frame()
}

// Drag presses at from, moves to to in steps, and lets go there.
func (s *Scene) Drag(from, to render.Point) {
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.Frame()
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: from.X + (to.X-from.X)*f, Y: from.Y + (to.Y-from.Y)*f}})
		s.Frame()
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: to, Buttons: render.ButtonPrimary})
	s.Frame()
	s.Frame()
}

// SelectAcross drags along the first run of text starting with prefix, as the pointer would, and answers what it
// selected.
func (s *Scene) SelectAcross(prefix string) string {
	s.T.Helper()
	at, ok := s.TextStart(prefix)
	if !ok {
		s.T.Fatalf("no %q:\n%s", prefix, s.Text())
	}
	y := at.Y - 5
	s.Drag(render.Point{X: at.X + 12, Y: y}, render.Point{X: at.X + 260, Y: y})
	return s.UI().SelectedText()
}

// TypeInto clicks the node named label and types text into it, a character at a time.
func (s *Scene) TypeInto(label, text string) {
	s.T.Helper()
	s.Click(Centre(s.Node(label).Bounds), 0)
	s.TypeText(text)
}

// TypeText types text on what has focus, a character at a time.
func (s *Scene) TypeText(text string) {
	for _, r := range text {
		s.Key(string(r))
	}
}

// PointerLeaves moves the pointer out of the window, as a person does once they've clicked, so nothing shows its hover.
func (s *Scene) PointerLeaves() {
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerLeave})
	s.Frame()
}
