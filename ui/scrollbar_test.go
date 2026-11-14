package ui

import (
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/render"
)

// scroller is a 200×100 scrolling column of n 20-pixel rows.
type scroller struct {
	u     *UI
	clk   *clocktest.Fake
	id    ID
	n     int
	ops   []render.Op
	build func(c *Ctx)
}

func newScroller(n int, bars Bars) *scroller {
	s := &scroller{u: newUI(), clk: clocktest.New(time.Unix(0, 0)), n: n}
	s.u.SetClock(s.clk)
	s.u.opts.ScrollThumb = render.Color{R: 1, G: 2, B: 3, A: 0x80}
	s.u.opts.ScrollThumbHot = render.Color{R: 4, G: 5, B: 6, A: 0xff}
	build := func(c *Ctx) {
		s.id = c.Column("list", Style{W: Px(200), H: Px(100), ScrollY: true, Bars: bars}, func(c *Ctx) {
			for i := range s.n {
				c.Box(i, Style{W: Flex(1), H: Px(20)}, nil)
			}
		})
	}
	s.build = build
	s.frame()
	s.frame()
	return s
}

func (s *scroller) frame() {
	var l render.List
	s.u.Frame(l.Record(1), render.Point{X: 400, Y: 300}, s.build)
	s.ops = l.Ops
}

// thumb is the scrollbar thumb drawn in the latest frame, if one was.
func (s *scroller) thumb() (render.Rect, render.Color, bool) {
	for _, op := range s.ops {
		if op.Kind == render.OpRect && op.Rect.Min.X >= 200-barInset-barWidthHot && op.Rect.Max.X <= 200 && op.Style.Fill.A > 0 {
			return op.Rect, op.Style.Fill, true
		}
	}
	return render.Rect{}, render.Color{}, false
}

// settle runs the clock on until what's moving has settled.
func (s *scroller) settle() {
	for range 30 {
		s.clk.Advance(10 * time.Millisecond)
		s.frame()
	}
}

func (s *scroller) move(x, y float32) {
	s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: x, Y: y}})
	s.frame()
}

func TestScrollbarShowsWhileHoveredOrScrolled(t *testing.T) {
	s := newScroller(20, BarsAuto)
	s.u.opts.ReducedMotion = false
	if _, _, ok := s.thumb(); ok {
		t.Fatal("a bar shows with the pointer elsewhere and no scroll")
	}
	s.move(50, 50)
	_, faint, _ := s.thumb()
	s.settle()
	r, full, ok := s.thumb()
	if !ok {
		t.Fatal("no bar while the pointer is over the list")
	}
	if faint.A >= full.A {
		t.Errorf("the bar didn't fade in: %d then %d", faint.A, full.A)
	}
	// 100 of 400 shows: the thumb is a quarter of the track.
	if h := r.Dy(); h < 23 || h > 25 {
		t.Errorf("thumb is %v tall, want a quarter of the 96px track", h)
	}
	s.u.Pointer(render.PointerEvent{Kind: render.PointerLeave})
	s.frame()
	s.settle()
	if _, _, ok := s.thumb(); ok {
		t.Error("the bar stayed after the pointer left")
	}
	s.u.ScrollBy(s.id, 0, 100)
	s.frame()
	s.settle()
	r, _, ok = s.thumb()
	if !ok || r.Min.Y < 25 {
		t.Fatalf("no bar, or not moved down, just after a scroll: %v %v", ok, r)
	}
	s.clk.Advance(barLinger)
	s.frame()
	s.settle()
	if _, _, ok := s.thumb(); ok {
		t.Error("the bar didn't fade after the scroll")
	}
}

func TestScrollbarOnlyWhenItOverflows(t *testing.T) {
	s := newScroller(4, BarsAlways)
	s.move(50, 50)
	if _, _, ok := s.thumb(); ok {
		t.Error("a bar shows on content that fits")
	}
}

func TestScrollbarModes(t *testing.T) {
	if _, _, ok := newScroller(20, BarsAlways).thumb(); !ok {
		t.Error("BarsAlways: no bar without the pointer")
	}
	h := newScroller(20, BarsHidden)
	h.move(50, 50)
	if _, _, ok := h.thumb(); ok {
		t.Error("BarsHidden: a bar shows")
	}
	a := newScroller(20, BarsAuto)
	a.u.opts.Bars = BarsAlways
	a.frame()
	if _, _, ok := a.thumb(); !ok {
		t.Error("the UI's BarsAlways doesn't apply to a BarsAuto box")
	}
}

func TestScrollbarDragAndPage(t *testing.T) {
	s := newScroller(20, BarsAuto)
	s.move(195, 10)
	r, col, ok := s.thumb()
	if !ok || col != s.u.opts.ScrollThumbHot {
		t.Fatalf("hovering the bar: %v, colour %v", ok, col)
	}
	// Drag the thumb down by half the track's free run: half way.
	s.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Buttons: render.ButtonPrimary, Pos: render.Point{X: 195, Y: r.Min.Y + 5}})
	s.frame()
	room := 96 - r.Dy()
	s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Buttons: render.ButtonPrimary, Pos: render.Point{X: 150, Y: r.Min.Y + 5 + room/2}})
	s.frame()
	if y := s.u.Scroll(s.id).Y; y < 145 || y > 155 {
		t.Errorf("dragged half way, scrolled %v, want 150 of 300", y)
	}
	s.u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Buttons: render.ButtonPrimary, Pos: render.Point{X: 150, Y: 60}})
	s.frame()
	// A press on the track below the thumb pages down.
	before := s.u.Scroll(s.id).Y
	s.move(195, 97)
	s.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Buttons: render.ButtonPrimary, Pos: render.Point{X: 195, Y: 97}})
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != min(before+100, 300) {
		t.Errorf("a press below the thumb scrolled from %v to %v, want a page", before, y)
	}
}

func TestScrollIntoView(t *testing.T) {
	s := newScroller(20, BarsAuto)
	want := 15
	b := s.build
	s.build = func(c *Ctx) {
		b(c)
		c.ScrollIntoView(s.id, Child(s.id, want))
	}
	s.frame()
	s.frame()
	// Row 15 spans 300–320 of the content; a 100-tall view shows it when scrolled to 220.
	if y := s.u.Scroll(s.id).Y; y != 220 {
		t.Errorf("scrolled to %v, want 220 (row 15 at the bottom)", y)
	}
	want = 2
	s.frame()
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 40 {
		t.Errorf("scrolled to %v, want 40 (row 2 at the top)", y)
	}
}

// A jump to a row out of view, or near its edge, puts it in the middle of the view; one in the middle half stays.
func TestCentreInView(t *testing.T) {
	s := newScroller(20, BarsAuto)
	want := 15
	b := s.build
	s.build = func(c *Ctx) {
		b(c)
		c.CentreInView(s.id, Child(s.id, want))
	}
	s.frame()
	s.frame()
	// Row 15 spans 300–320 of the content; in the middle of a 100-tall view when scrolled to 260.
	if y := s.u.Scroll(s.id).Y; y != 260 {
		t.Errorf("scrolled to %v, want 260 (row 15 in the middle)", y)
	}
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 260 {
		t.Errorf("row 15 was in the middle already, but the view scrolled to %v", y)
	}
	want = 13 // at the top edge of the view
	s.frame()
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 220 {
		t.Errorf("scrolled to %v, want 220 (row 13 in the middle)", y)
	}
}

// A node built this frame for the first time has no rect until it's laid out: not one at the origin.
func TestRectOfANodeNotLaidOut(t *testing.T) {
	u := newUI()
	var rows int
	var laid []bool
	build := func(c *Ctx) {
		id := c.Column("list", Style{W: Px(200), H: Px(100)}, func(c *Ctx) {
			for i := range rows {
				c.Box(i, Style{W: Flex(1), H: Px(20)}, nil)
			}
		})
		laid = laid[:0]
		for i := range rows {
			_, ok := c.Rect(Child(id, i))
			laid = append(laid, ok)
		}
	}
	rows = 1
	u.Layout(render.Point{X: 200, Y: 100}, build)
	rows = 2
	u.Layout(render.Point{X: 200, Y: 100}, build)
	if !laid[0] || laid[1] {
		t.Errorf("laid out: %v, want the first row only", laid)
	}
}

// A Follow box stays at its end as it grows, stays where the wheel or the bar leaves it, and follows again once
// scrolled back to its end.
func TestFollowLetsGoWhenScrolled(t *testing.T) {
	s := newScroller(20, BarsAuto)
	opts := s.u.opts
	s.u = newUI()
	s.u.SetClock(s.clk)
	s.u.opts = opts
	s.build = func(c *Ctx) {
		s.id = c.Column("list", Style{W: Px(200), H: Px(100), ScrollY: true, Follow: true}, func(c *Ctx) {
			for i := range s.n {
				c.Box(i, Style{W: Flex(1), H: Px(20)}, nil)
			}
		})
	}
	s.frame()
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 300 {
		t.Fatalf("opened at %v, want its end, 300", y)
	}
	s.n = 25
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 400 {
		t.Fatalf("grown, at %v, want its new end, 400", y)
	}
	s.move(50, 50)
	s.u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 50}, Scroll: render.Point{Y: -150}})
	s.frame()
	s.n = 30
	s.frame()
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 250 {
		t.Fatalf("wheeled up to 250 and grown, at %v", y)
	}
	s.move(195, 10)
	r, _, _ := s.thumb()
	s.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Buttons: render.ButtonPrimary, Pos: render.Point{X: 195, Y: r.Min.Y + 5}})
	s.frame()
	s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Buttons: render.ButtonPrimary, Pos: render.Point{X: 195, Y: r.Min.Y - 20}})
	s.frame()
	s.frame()
	if y := s.u.Scroll(s.id).Y; y >= 250 {
		t.Fatalf("the thumb dragged up, at %v, not above 250", y)
	}
	s.u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Buttons: render.ButtonPrimary, Pos: render.Point{X: 195, Y: 10}})
	s.u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 50}, Scroll: render.Point{Y: 1000}})
	s.frame()
	s.n = 35
	s.frame()
	if y := s.u.Scroll(s.id).Y; y != 600 {
		t.Errorf("back at the end and grown, at %v, want 600", y)
	}
}
