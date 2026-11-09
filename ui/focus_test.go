package ui

import (
	"testing"

	"github.com/liamg/oriel/render"
)

// focusScene is a pane with two fields, the second its default target, and a dialog over it while open.
type focusScene struct {
	u                   *UI
	pane, first, second ID
	dialog, dialogField ID
	open, into, gone    bool
	firstGone, blur     bool
	request             ID
}

func (s *focusScene) frame() {
	paintOps(s.u, render.Point{X: 400, Y: 300}, func(c *Ctx) {
		s.pane = c.Column("pane", Style{W: Px(200), H: Px(200), Gap: 10}, func(c *Ctx) {
			if !s.firstGone {
				s.first = c.Box("first", Style{W: Px(100), H: Px(20), Focusable: true}, nil)
			}
			if !s.gone {
				s.second = c.Box("second", Style{W: Px(100), H: Px(20), Focusable: true, DefaultFocus: true}, nil)
			}
		})
		if s.blur {
			c.Blur()
			s.blur = false
		}
		if s.into {
			c.FocusInto(s.pane)
			s.into = false
		}
		if s.request != 0 {
			c.RequestFocus(s.request)
			s.request = 0
		}
		if !s.open {
			return
		}
		s.dialog = c.Column("dialog", Style{W: Px(100), H: Px(100), Z: LayerDialog, Trap: true, Focusable: true, Float: &Float{Point: &render.Point{X: 250}}},
			func(c *Ctx) {
				s.dialogField = c.Box("field", Style{W: Px(80), H: Px(20), Focusable: true, DefaultFocus: true}, nil)
			})
	})
}

func (s *focusScene) click(id ID) {
	r := s.u.byID[id].rect()
	p := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
	s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary})
	s.frame()
	s.u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary})
	s.frame()
}

func newFocusScene() *focusScene {
	s := &focusScene{u: newUI()}
	s.frame()
	return s
}

// Entering a pane focuses its default target, once: a click elsewhere in it then keeps focus.
func TestFocusIntoGoesToTheDefaultOnce(t *testing.T) {
	s := newFocusScene()
	s.into = true
	s.frame()
	if f := s.u.Focused(); f != s.second {
		t.Fatalf("after entering the pane focus is on %v, want its default %v", f, s.second)
	}
	s.click(s.first)
	for range 10 {
		s.frame()
	}
	if f := s.u.Focused(); f != s.first {
		t.Errorf("focus moved off the field clicked to %v", f)
	}
	s.into = true
	s.frame()
	if f := s.u.Focused(); f != s.first {
		t.Errorf("entering a pane that already holds focus moved it to %v", f)
	}
}

// A request is taken once its target is built, and dropped if the user presses or types first.
func TestRequestFocusOnce(t *testing.T) {
	s := newFocusScene()
	s.request = s.first
	s.frame()
	if f := s.u.Focused(); f != s.first {
		t.Fatalf("focus = %v, want the requested %v", f, s.first)
	}
	s.click(s.second)
	s.frame()
	if f := s.u.Focused(); f != s.second {
		t.Errorf("a request was asked again: focus = %v, want the clicked %v", f, s.second)
	}

	s.gone = true
	s.frame()
	s.request = s.second
	s.frame()
	s.u.Chord() // a key typed before the target is built
	s.gone = false
	s.frame()
	if f := s.u.Focused(); f == s.second {
		t.Error("a request taken after the user typed elsewhere")
	}
}

// Closing a dialog gives focus back to what had it before it opened; with that gone, to the default target.
func TestOverlayCloseRestoresFocus(t *testing.T) {
	s := newFocusScene()
	s.click(s.first)
	s.open = true
	s.frame()
	if f := s.u.Focused(); f != s.dialogField {
		t.Fatalf("a dialog opened with focus on %v, want its default %v", f, s.dialogField)
	}
	s.open = false
	s.frame()
	if f := s.u.Focused(); f != s.first {
		t.Errorf("after the dialog closed focus is on %v, want %v, which had it before", f, s.first)
	}

	s.open = true
	s.frame()
	s.click(s.dialogField)
	s.u.focusOff(s.first)
	s.open = false
	s.frame()
	if f := s.u.Focused(); f != s.second {
		t.Errorf("with what had focus before gone, focus is on %v, want the default %v", f, s.second)
	}
}

// focusOff forgets id held focus, as if it had gone since.
func (u *UI) focusOff(id ID) {
	for i, t := range u.in.trail {
		if t == id {
			u.in.trail = append(u.in.trail[:i:i], u.in.trail[i+1:]...)
		}
	}
}

// A focused node that goes hands focus to the default target of what it was in.
func TestVanishedFocusGoesToTheDefault(t *testing.T) {
	s := newFocusScene()
	s.click(s.first)
	s.firstGone = true
	s.frame()
	if f := s.u.Focused(); f != s.second {
		t.Errorf("focus = %v, want the pane's default %v", f, s.second)
	}
}

// Entering a pane still loading focuses its default target when it's built, unless the user moved on first.
func TestFocusIntoWaitsForTheDefault(t *testing.T) {
	for _, typed := range []bool{false, true} {
		s := newFocusScene()
		s.gone, s.into = true, true
		s.frame()
		s.frame()
		if typed {
			s.u.Chord()
		}
		s.gone = false
		s.frame()
		if got := s.u.Focused() == s.second; got == typed {
			t.Errorf("typed first %v: focus on the default once built is %v", typed, got)
		}
	}
}

// A request asked while a dialog holds focus waits for it to close.
func TestRequestFocusWaitsUnderADialog(t *testing.T) {
	s := newFocusScene()
	s.open = true
	s.frame()
	s.request = s.first
	s.frame()
	if f := s.u.Focused(); f != s.dialogField {
		t.Fatalf("a request from under the dialog took focus to %v", f)
	}
	s.open = false
	s.frame()
	if f := s.u.Focused(); f != s.first {
		t.Errorf("with the dialog closed focus is on %v, want the requested %v", f, s.first)
	}
}

// Blur hands focus to the default target of what had it.
func TestBlurGoesToTheDefault(t *testing.T) {
	s := newFocusScene()
	s.click(s.first)
	s.blur = true
	s.frame()
	if f := s.u.Focused(); f != s.second {
		t.Errorf("after a blur focus is on %v, want the pane's default %v", f, s.second)
	}
}

// A card marked FocusOnOpen takes focus the frame it appears, from a field that had it, without trapping it: focus
// moved off it stays moved. Once it goes, focus is back where it was.
func TestACardThatOpensTakesFocusAndGivesItBack(t *testing.T) {
	u := newUI()
	var field, other, card ID
	shown := false
	frame := func() {
		paintOps(u, render.Point{X: 400, Y: 300}, func(c *Ctx) {
			field = c.Box("field", Style{W: Px(100), H: Px(20), Focusable: true}, nil)
			other = c.Box("other", Style{W: Px(100), H: Px(20), Focusable: true}, nil)
			c.Column("banner", Style{W: Px(200), Z: LayerToast, Float: &Float{Point: &render.Point{X: 150}}}, func(c *Ctx) {
				if shown {
					card = c.Column("card", Style{W: Px(180), H: Px(40), Focusable: true, FocusOnOpen: true}, func(c *Ctx) {
						c.Box("allow", Style{W: Px(60), H: Px(20), Focusable: true, DefaultFocus: true}, nil)
					})
				}
			})
		})
	}
	click := func(id ID) {
		r := u.byID[id].rect()
		p := render.Point{X: r.Min.X + 5, Y: r.Min.Y + 5}
		u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary})
		u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary})
		frame()
	}
	frame()
	click(field)
	shown = true
	frame()
	frame()
	if !u.within(u.Focused(), card) {
		t.Fatalf("focus %v, want in the card that opened (%v)", u.Focused(), card)
	}
	shown = false
	frame()
	frame()
	if u.Focused() != field {
		t.Errorf("once the card went, focus %v, want back in the field (%v)", u.Focused(), field)
	}

	shown = true
	frame()
	click(other)
	frame()
	if u.Focused() != other {
		t.Errorf("the card kept focus from a click elsewhere: %v, want %v", u.Focused(), other)
	}
}

// A page opened after a card took focus as it appeared gets focus, though the card is on a higher layer: the card
// doesn't hold focus from what's opened after it.
func TestAPageOpenedAfterACardThatTookFocusGetsIt(t *testing.T) {
	for _, trap := range []bool{false, true} {
		pageOpenedAfterACard(t, trap)
	}
}

func pageOpenedAfterACard(t *testing.T, trap bool) {
	t.Helper()
	u := newUI()
	var card, pageField ID
	page := false
	frame := func(into bool) {
		paintOps(u, render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Box("field", Style{W: Px(100), H: Px(20), Focusable: true}, nil)
			if page {
				p := c.Column("page", Style{W: Px(300), H: Px(200), Z: LayerPage, Trap: trap, Focusable: true, Float: &Float{Point: &render.Point{}}}, func(c *Ctx) {
					pageField = c.Box("filter", Style{W: Px(100), H: Px(20), Focusable: true, DefaultFocus: true}, nil)
				})
				if into {
					c.FocusInto(p)
				}
			}
			c.Column("banner", Style{W: Px(200), Z: LayerToast, Float: &Float{Point: &render.Point{X: 150}}}, func(c *Ctx) {
				card = c.Column("card", Style{W: Px(180), H: Px(40), Focusable: true, FocusOnOpen: true}, nil)
			})
		})
	}
	frame(false)
	frame(false)
	if u.Focused() != card {
		t.Fatalf("focus %v, want the card (%v)", u.Focused(), card)
	}
	page = true
	frame(!trap) // a trap takes focus on its own; a page that doesn't asks for it
	frame(false)
	if u.Focused() != pageField {
		t.Errorf("trap %v: focus %v after the page opened, want its field %v", trap, u.Focused(), pageField)
	}
}
