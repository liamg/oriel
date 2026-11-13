package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

var (
	idle    = render.Color{R: 0x19, G: 0x18, B: 0x15, A: 255}
	hovered = render.Color{R: 0x23, G: 0x21, B: 0x1c, A: 255}
	pressed = render.Color{R: 0x33, G: 0x2f, B: 0x27, A: 255}
	muted   = render.Color{R: 0x12, G: 0x12, B: 0x10, A: 255}
)

// button is a stand-in widget: it styles itself from its interaction, as the toolkit's widgets do.
func button(c *Ctx, key string, disabled bool) Interaction {
	id := c.Next(key)
	in := c.Interaction(id)
	fill := idle
	switch {
	case in.Disabled:
		fill = muted
	case in.Pressed:
		fill = pressed
	case in.Hovered:
		fill = hovered
	}
	c.Row(key, Style{W: Px(80), H: Px(24), Pad: PadXY(8, 5), Fill: fill, Radius: 4, Focusable: true, Disabled: disabled, NoHover: true,
		Cursor: render.CursorPointer, Tooltip: "Save the file"}, func(c *Ctx) {
		c.Text(nil, key, TextStyle{})
	})
	return in
}

func screen(c *Ctx) {
	c.Row("bar", Style{Pad: Pad(10), Gap: 10}, func(c *Ctx) {
		button(c, "save", false)
		button(c, "quit", true)
	})
}

type harness struct {
	u   *UI
	clk *clock
	ops []render.Op
}

func newHarness() *harness {
	h := &harness{u: newUI(), clk: &clock{t: time.Unix(1000, 0)}}
	h.u.SetClock(h.clk.now)
	h.frame()
	return h
}

func (h *harness) frame() string {
	h.ops = paintOps(h.u, render.Point{X: 300, Y: 100}, screen)
	return describe(h.ops)
}

func (h *harness) pointer(k render.PointerKind, x, y float32) {
	h.u.Pointer(render.PointerEvent{Kind: k, Pos: render.Point{X: x, Y: y}, Buttons: render.ButtonPrimary})
}

// TestStateSnapshots pins the display list in each state: idle, hovered, pressed, focused from the
// keyboard, disabled under the pointer, and with the tooltip up.
func TestStateSnapshots(t *testing.T) {
	h := newHarness()
	var b strings.Builder
	snap := func(name string) { fmt.Fprintf(&b, "=== %s\n%s\n", name, h.frame()) }

	snap("idle")
	h.pointer(render.PointerMove, 20, 20)
	snap("hovered save")
	h.pointer(render.PointerPress, 20, 20)
	snap("pressed save")
	h.pointer(render.PointerRelease, 20, 20)
	h.pointer(render.PointerLeave, 0, 0)
	h.u.FocusNext(1)
	snap("save focused from the keyboard")
	h.pointer(render.PointerMove, 110, 20)
	if c := h.u.Cursor(); c != render.CursorNotAllowed {
		t.Errorf("cursor over a disabled button is %v", c)
	}
	snap("disabled quit under the pointer")
	h.pointer(render.PointerMove, 20, 20)
	h.frame()
	h.clk.advance(400 * time.Millisecond)
	if strings.Contains(h.frame(), "Save the file") {
		t.Error("tooltip before its delay")
	}
	if want := h.clk.t.Add(100 * time.Millisecond); !h.u.NextFrame().Equal(want) {
		t.Errorf("next frame at %v, want the tooltip's delay at %v", h.u.NextFrame(), want)
	}
	h.clk.advance(100 * time.Millisecond)
	snap("tooltip")

	path := filepath.Join("testdata", "states.golden")
	if *update {
		os.WriteFile(path, []byte(b.String()), 0o644)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.String() != string(want) {
		t.Errorf("states:\n%s\nwant:\n%s", b.String(), want)
	}
}

// A frame a hovered control asks for now isn't held back to its tooltip's delay.
func TestTooltipKeepsAnEarlierWake(t *testing.T) {
	h := newHarness()
	h.pointer(render.PointerMove, 20, 20)
	paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
		screen(c)
		c.WakeAt(c.Now())
	})
	if got := h.u.NextFrame(); !got.Equal(h.clk.t) {
		t.Errorf("next frame at %v, want now (%v)", got, h.clk.t)
	}
}

func TestClicks(t *testing.T) {
	h := newHarness()
	click := func() bool {
		var got bool
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
			c.Row("bar", Style{Pad: Pad(10), Gap: 10}, func(c *Ctx) {
				got = button(c, "save", false).Clicked
				button(c, "quit", true)
			})
		})
		return got
	}
	h.pointer(render.PointerMove, 20, 20)
	h.pointer(render.PointerPress, 20, 20)
	h.pointer(render.PointerRelease, 20, 20)
	if !click() {
		t.Error("press and release on save didn't click it")
	}
	if click() {
		t.Error("a click lasted two frames")
	}
	h.pointer(render.PointerPress, 20, 20)
	h.pointer(render.PointerMove, 250, 20)
	h.pointer(render.PointerRelease, 250, 20)
	if click() {
		t.Error("released off the button, and it still clicked")
	}
	h.pointer(render.PointerMove, 110, 20)
	h.pointer(render.PointerPress, 110, 20)
	h.pointer(render.PointerRelease, 110, 20)
	if in := h.u.Interaction(h.u.byID[childID(childID(0, "bar"), "quit")].id); in.Clicked || !in.Disabled {
		t.Errorf("disabled quit: %+v", in)
	}
}

func TestFocusTraversal(t *testing.T) {
	u := newUI()
	var ids []ID
	build := func(c *Ctx) {
		ids = ids[:0]
		c.Row("r", Style{}, func(c *Ctx) {
			for i := range 4 {
				ids = append(ids, c.Box(i, Style{W: Px(10), H: Px(10), Focusable: true, Disabled: i == 2}, nil))
			}
		})
	}
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	var order []ID
	for range 4 {
		u.FocusNext(1)
		order = append(order, u.Focused())
	}
	if want := []ID{ids[0], ids[1], ids[3], ids[0]}; !slices.Equal(order, want) {
		t.Errorf("tab order %v, want %v (skipping the disabled one, wrapping)", order, want)
	}
	u.FocusNext(-1)
	if u.Focused() != ids[3] {
		t.Error("shift-tab didn't go back")
	}
	paintOps(u, render.Point{X: 100, Y: 100}, func(c *Ctx) {})
	if u.Focused() != 0 {
		t.Error("focus stayed on a node that's gone")
	}
}

func TestWheelScrollsWhatsUnderThePointer(t *testing.T) {
	u := newUI()
	var inner, outer ID
	build := func(c *Ctx) {
		outer = c.Column("outer", Style{W: Px(200), H: Px(100), ScrollY: true}, func(c *Ctx) {
			inner = c.Row("inner", Style{W: Px(100), H: Px(40), ScrollX: true}, func(c *Ctx) {
				c.Box("wide", Style{W: Px(400), H: Px(10)}, nil)
			})
			c.Box("tall", Style{W: Px(10), H: Px(400)}, nil)
		})
	}
	paintOps(u, render.Point{X: 300, Y: 300}, build)
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 20}, Scroll: render.Point{Y: 30}})
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 20}, Scroll: render.Point{X: 25}})
	paintOps(u, render.Point{X: 300, Y: 300}, build)
	if got := u.Scroll(outer); got != (render.Point{Y: 30}) {
		t.Errorf("outer scrolled to %v; a vertical wheel over a horizontal scroller goes to the vertical one", got)
	}
	if got := u.Scroll(inner); got != (render.Point{X: 25}) {
		t.Errorf("inner scrolled to %v", got)
	}

	// A trackpad's diagonal move over it scrolls each its own way: across the inner, down the outer.
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 5}, Scroll: render.Point{X: 10, Y: 20}})
	paintOps(u, render.Point{X: 300, Y: 300}, build)
	if got, got2 := u.Scroll(inner), u.Scroll(outer); got != (render.Point{X: 35}) || got2 != (render.Point{Y: 50}) {
		t.Errorf("a diagonal move left inner at %v and outer at %v, want {35 0} and {0 50}", got, got2)
	}
}

func TestSprings(t *testing.T) {
	u := newUI()
	u.opts.ReducedMotion = false
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	target := float32(0)
	var x float32
	frame := func() { u.Layout(render.Point{X: 10, Y: 10}, func(c *Ctx) { x = c.Anim("slide", target) }) }
	frame()
	if x != 0 || !u.NextFrame().IsZero() {
		t.Fatalf("a new spring starts at its target and settled: %v, next frame %v", x, u.NextFrame())
	}
	target = 100
	var xs []float32
	for range 60 {
		clk.advance(time.Second / 120)
		frame()
		xs = append(xs, x)
	}
	if xs[0] <= 0 || xs[0] >= 50 {
		t.Errorf("first step %v: it should move, not jump", xs[0])
	}
	for i := 1; i < len(xs); i++ {
		if xs[i] < xs[i-1] || xs[i] > 100 {
			t.Fatalf("critically damped springs neither overshoot nor turn back: %v", xs)
		}
	}
	if x != 100 || !u.NextFrame().IsZero() {
		t.Errorf("after half a second at 120 Hz: %v, next frame %v; want settled at 100", x, u.NextFrame())
	}

	o := u.Options()
	o.ReducedMotion = true
	u.SetOptions(o)
	target = 0
	clk.advance(time.Second / 120)
	frame()
	if x != 0 || !u.NextFrame().IsZero() {
		t.Errorf("with reduced motion: %v, next frame %v; want 0 at once", x, u.NextFrame())
	}
}

// A fork builds and paints the same tree with another theme (or anything else) and leaves the original as it was:
// its scroll offsets carry over, its rects and state don't change.
func TestFork(t *testing.T) {
	u := newUI()
	var outer ID
	build := func(wide bool) func(c *Ctx) {
		return func(c *Ctx) {
			outer = c.Column("outer", Style{W: Px(200), H: Px(100), ScrollY: true}, func(c *Ctx) {
				w := float32(10)
				if wide {
					w = 150
				}
				c.Box("tall", Style{W: Px(w), H: Px(400)}, nil)
			})
		}
	}
	paintOps(u, render.Point{X: 300, Y: 300}, build(false))
	u.ScrollBy(outer, 0, 30)
	paintOps(u, render.Point{X: 300, Y: 300}, build(false))
	before, _ := u.Rect(u.Hit(render.Point{X: 5, Y: 5})[0])
	f := u.Fork()
	paintOps(f, render.Point{X: 300, Y: 300}, build(true))
	if got := f.Scroll(outer); got != (render.Point{Y: 30}) {
		t.Errorf("the fork scrolled to %v", got)
	}
	if ids := f.Hit(render.Point{X: 100, Y: 5}); len(ids) == 0 {
		t.Error("the fork's own layout isn't hit")
	}
	f.ScrollBy(outer, 0, 50)
	if got := u.Scroll(outer); got != (render.Point{Y: 30}) {
		t.Errorf("scrolling the fork moved the original to %v", got)
	}
	if after, _ := u.Rect(u.Hit(render.Point{X: 5, Y: 5})[0]); after != before {
		t.Errorf("the original's rect went from %v to %v", before, after)
	}
	if ids := u.Hit(render.Point{X: 100, Y: 5}); len(ids) != 1 {
		t.Errorf("the original hits %v at x=100; only outer is there", ids)
	}
}

// A clickable box with no hover of its own gets the toolkit's: a wash under the pointer, a stronger one pressed.
func TestHoverWash(t *testing.T) {
	u := newUI()
	o := u.Options()
	o.Hover, o.Pressed = render.Color{R: 1, A: 10}, render.Color{R: 2, A: 20}
	u.SetOptions(o)
	boxes := []Style{
		{Interactive: true, Cursor: render.CursorPointer},
		{Focusable: true, Cursor: render.CursorPointer},
		{Interactive: true, Cursor: render.CursorPointer, NoHover: true},
		{Interactive: true, Cursor: render.CursorText},
		{Interactive: true, Cursor: render.CursorPointer, Disabled: true},
		{},
	}
	frame := func() []render.Op {
		return paintOps(u, render.Point{X: 600, Y: 40}, func(c *Ctx) {
			c.Row("row", Style{}, func(c *Ctx) {
				for i, st := range boxes {
					st.W, st.H, st.Fill = Px(100), Px(40), render.Color{B: 9, A: 255}
					c.Box(i, st, nil)
				}
			})
		})
	}
	washes := func(ops []render.Op) []render.Color {
		var out []render.Color
		for _, op := range ops {
			if op.Kind == render.OpRect && (op.Style.Fill == o.Hover || op.Style.Fill == o.Pressed) {
				out = append(out, op.Style.Fill)
			}
		}
		return out
	}
	frame()
	for i, want := range []bool{true, true, false, false, false, false} {
		p := render.Point{X: float32(i)*100 + 50, Y: 20}
		u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
		if got := washes(frame()); (len(got) == 1 && got[0] == o.Hover) != want || len(got) > 1 {
			t.Errorf("box %d hovered: washes %v, want one: %v", i, got, want)
		}
		u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary})
		if got := washes(frame()); (len(got) == 1 && got[0] == o.Pressed) != want || len(got) > 1 {
			t.Errorf("box %d pressed: washes %v, want the pressed one: %v", i, got, want)
		}
		u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary})
	}
	u.Pointer(render.PointerEvent{Kind: render.PointerLeave})
	if got := washes(frame()); len(got) != 0 {
		t.Errorf("with the pointer gone: washes %v", got)
	}
}

// Inside holds while the pointer's on a box or on something interactive inside it, as a button in a row.
func TestInside(t *testing.T) {
	u := newUI()
	var row, btn Interaction
	frame := func() {
		paintOps(u, render.Point{X: 300, Y: 40}, func(c *Ctx) {
			row = c.Interaction(c.Next("row"))
			c.Row("row", Style{W: Px(200), H: Px(30), Interactive: true}, func(c *Ctx) {
				btn = c.Interaction(c.Next("x"))
				c.Box("gap", Style{W: Px(170)}, nil)
				c.Box("x", Style{W: Px(20), H: Px(20), Interactive: true}, nil)
			})
		})
	}
	frame()
	for _, tt := range []struct {
		x               float32
		inRow, rowHover bool
	}{{50, true, true}, {180, true, false}, {250, false, false}} {
		u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: tt.x, Y: 15}})
		frame()
		if row.Inside != tt.inRow || row.Hovered != tt.rowHover {
			t.Errorf("at x=%v: row inside %v hovered %v, want %v %v", tt.x, row.Inside, row.Hovered, tt.inRow, tt.rowHover)
		}
		if tt.x == 180 && (!btn.Hovered || !btn.Inside) {
			t.Errorf("on the button: %+v", btn)
		}
	}
}

// A press on a button inside a row is within the row, though the row isn't what's pressed.
func TestPressedWithin(t *testing.T) {
	h := newHarness()
	within := func() (bar, other bool) {
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
			id := c.Next("bar")
			bar = c.PressedWithin(id)
			c.Row("bar", Style{Pad: Pad(10), Gap: 10}, func(c *Ctx) { button(c, "save", false) })
			other = c.PressedWithin(c.Next("other"))
			c.Box("other", Style{W: Px(10), H: Px(10)}, nil)
		})
		return
	}
	within()
	if bar, _ := within(); bar {
		t.Error("pressed within before a press")
	}
	h.pointer(render.PointerMove, 20, 20)
	h.pointer(render.PointerPress, 20, 20)
	if bar, other := within(); !bar || other {
		t.Errorf("a press on save: within the bar %v, within another box %v", bar, other)
	}
	h.pointer(render.PointerRelease, 20, 20)
	if bar, _ := within(); bar {
		t.Error("still pressed within after the release")
	}
}

// A modifier held alone is held alone from when it went down until a key or a press joins it, or the set held
// changes; letting go of every modifier starts afresh.
func TestHeldAlone(t *testing.T) {
	h := newHarness()
	alone := func() (m render.Mods, since time.Time) {
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) { m, since = c.HeldAlone() })
		return
	}
	if m, _ := alone(); m != 0 {
		t.Fatalf("held alone before anything's down: %v", m)
	}
	h.u.Chord() // a key typed with nothing held
	h.u.Mods(render.ModAlt)
	t0 := h.clk.t
	h.clk.advance(300 * time.Millisecond)
	if m, since := alone(); m != render.ModAlt || !since.Equal(t0) {
		t.Errorf("⌥ down: %v since %v, want ⌥ since %v", m, since, t0)
	}
	h.u.Chord() // ⌥O
	if m, _ := alone(); m != 0 {
		t.Errorf("⌥ with a key is a chord, not held alone: %v", m)
	}
	h.u.Mods(0)
	h.u.Mods(render.ModAlt)
	if m, _ := alone(); m != render.ModAlt {
		t.Errorf("⌥ again after letting go: %v", m)
	}
	h.u.Mods(render.ModAlt | render.ModSuper)
	if m, since := alone(); m != render.ModAlt|render.ModSuper || !since.Equal(h.clk.t) {
		t.Errorf("⌥⌘: %v since %v", m, since)
	}
	h.u.Mods(render.ModAlt)
	h.pointer(render.PointerPress, 20, 20)
	if m, _ := alone(); m != 0 {
		t.Errorf("a press while ⌥ is down: %v", m)
	}
	paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
		if c.Held() != render.ModAlt {
			t.Errorf("held: %v", c.Held())
		}
	})
}

// LastKey is when a key last went down in the window, on the UI's clock, whether or not anything took it, and
// whatever has focus; a press of the pointer isn't a key.
func TestLastKey(t *testing.T) {
	h := newHarness()
	last := func() (at time.Time) {
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) { at = c.LastKey() })
		return
	}
	if at := last(); !at.IsZero() {
		t.Fatalf("a last key before any: %v", at)
	}
	h.u.KeyDown()
	t0 := h.clk.t
	h.clk.advance(2 * time.Second)
	if at := last(); !at.Equal(t0) {
		t.Errorf("the last key at %v, want %v", at, t0)
	}
	h.pointer(render.PointerPress, 20, 20)
	h.pointer(render.PointerRelease, 20, 20)
	if at := last(); !at.Equal(t0) {
		t.Errorf("a press moved the last key to %v", at)
	}
	h.u.KeyDown()
	if at := last(); !at.Equal(h.clk.t) {
		t.Errorf("the last key at %v, want %v", at, h.clk.t)
	}
}

// A focused node with Text takes typed text; one without, or disabled, or that takes no keys, doesn't.
func TestTakesText(t *testing.T) {
	u := newUI()
	var ids []ID
	build := func(c *Ctx) {
		ids = ids[:0]
		c.Row("r", Style{}, func(c *Ctx) {
			ids = append(ids, c.Box("field", Style{W: Px(10), H: Px(10), Focusable: true, KeyMode: "input", Text: true}, nil))
			ids = append(ids, c.Box("list", Style{W: Px(10), H: Px(10), Focusable: true, KeyMode: "list"}, nil))
			ids = append(ids, c.Box("bare", Style{W: Px(10), H: Px(10), Focusable: true, Text: true}, nil))
		})
	}
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if u.TakesText() {
		t.Error("nothing is focused, yet it takes text")
	}
	for i, want := range []bool{true, false, false} {
		u.Focus(ids[i])
		paintOps(u, render.Point{X: 100, Y: 100}, build)
		if got := u.TakesText(); got != want {
			t.Errorf("node %d takes text: %v, want %v", i, got, want)
		}
	}
}

// A pointer move needs a frame only when it changes something the latest frame read: what's hovered, the pointer
// itself where a node asked for it, or a region a scrollbar tested.
func TestPointerMovesThatChangeNothing(t *testing.T) {
	h := newHarness()
	move := func(x, y float32, b render.Buttons) bool {
		return h.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: x, Y: y}, Buttons: b})
	}
	move(250, 80, 0)
	h.frame()
	if move(260, 85, 0) {
		t.Error("a move over nothing interactive asks for a frame")
	}
	if !move(20, 20, 0) {
		t.Error("a move onto a button doesn't ask for a frame")
	}
	h.frame()
	if move(22, 21, 0) {
		t.Error("a move within the hovered button asks for a frame")
	}
	if !move(23, 21, render.ButtonPrimary) {
		t.Error("a move with a button held doesn't ask for a frame")
	}
	h.clk.advance(time.Second)
	if !strings.Contains(h.frame(), "Save the file") {
		t.Fatal("no tooltip")
	}
	if !move(24, 22, 0) {
		t.Error("a move under a tooltip, which follows the pointer, doesn't ask for a frame")
	}

	var at render.Point
	read := func(c *Ctx) { at = c.Interaction(c.Next("canvas")).Pointer() }
	paintOps(h.u, render.Point{X: 300, Y: 100}, read)
	if !move(30, 30, 0) {
		t.Error("a move doesn't ask for a frame where the last one read the pointer")
	}
	paintOps(h.u, render.Point{X: 300, Y: 100}, read)
	if at != (render.Point{X: 30, Y: 30}) {
		t.Errorf("Interaction.Pointer is %v", at)
	}

	s := newScroller(20, BarsAuto)
	s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 200}})
	s.frame()
	if s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 310, Y: 210}}) {
		t.Error("a move outside a scrolling box asks for a frame")
	}
	if !s.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 50, Y: 50}}) {
		t.Error("a move into a scrolling box, whose scrollbar shows on hover, doesn't ask for a frame")
	}
}

// A dialog opened over another on the same layer holds focus, though the first one's box is a float nested in its
// scrim: within a layer, floats paint (and trap) in the order they were built.
func TestDialogOverANestedDialogHoldsFocus(t *testing.T) {
	u := newUI()
	var field ID
	build := func(c *Ctx) {
		c.Box("first.scrim", Style{W: Px(200), H: Px(200), Z: LayerDialog, Float: &Float{Point: &render.Point{}}}, func(c *Ctx) {
			c.Column("first", Style{W: Px(100), H: Px(100), Z: LayerDialog, Trap: true, Focusable: true, Float: &Float{Point: &render.Point{}}}, nil)
		})
		c.Column("second", Style{W: Px(80), H: Px(80), Z: LayerDialog, Trap: true, Focusable: true, Float: &Float{Point: &render.Point{X: 10, Y: 10}}},
			func(c *Ctx) {
				field = c.Box("field", Style{W: Px(20), H: Px(20), Focusable: true}, nil)
			})
	}
	paintOps(u, render.Point{X: 300, Y: 300}, build)
	u.Focus(field)
	paintOps(u, render.Point{X: 300, Y: 300}, build)
	if u.Focused() != field {
		t.Errorf("focus = %v, want the second dialog's field %v", u.Focused(), field)
	}
}

// A second click on the same node within DoubleClick is a double-click; two slow ones, or on different nodes, aren't.
func TestDoubleClick(t *testing.T) {
	h := newHarness()
	var save, quit Interaction
	build := func() {
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
			c.Row("bar", Style{Pad: Pad(10), Gap: 10}, func(c *Ctx) {
				save = button(c, "save", false)
				quit = button(c, "quit", false)
			})
		})
	}
	click := func(x float32) {
		h.pointer(render.PointerMove, x, 20)
		h.pointer(render.PointerPress, x, 20)
		h.pointer(render.PointerRelease, x, 20)
		build()
	}
	build()
	click(20)
	if !save.Clicked || save.DoubleClicked {
		t.Fatalf("one click: clicked %v, double %v", save.Clicked, save.DoubleClicked)
	}
	h.clk.advance(DoubleClick / 2)
	click(20)
	if !save.Clicked || !save.DoubleClicked {
		t.Errorf("a second click soon after: clicked %v, double %v", save.Clicked, save.DoubleClicked)
	}
	h.clk.advance(DoubleClick / 2)
	click(20)
	if save.DoubleClicked {
		t.Error("a third click made a second double-click")
	}
	h.clk.advance(DoubleClick * 2)
	click(20)
	if save.DoubleClicked {
		t.Error("two slow clicks made a double-click")
	}
	h.clk.advance(DoubleClick / 4)
	click(110)
	if quit.DoubleClicked || !quit.Clicked {
		t.Errorf("a click on another node after one on save: double %v", quit.DoubleClicked)
	}
}

// Text with a tooltip in a clickable row shows its tooltip once the pointer rests on it, and a click on it is the row's.
func TestTooltipInAClickableRow(t *testing.T) {
	clk := &clock{t: time.Unix(1000, 0)}
	u := newUI()
	u.SetClock(clk.now)
	var row Interaction
	frame := func() string {
		return describe(paintOps(u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
			id := c.Next("row")
			row = c.Interaction(id)
			c.Row("row", Style{W: Px(200), H: Px(30), Interactive: true}, func(c *Ctx) {
				c.Box("age", Style{W: Px(40), H: Px(20), Tooltip: "Mon 2 Nov 2026, 14:03:12"}, func(c *Ctx) { c.Text("t", "5m", body) })
			})
		}))
	}
	frame()
	if !u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 10, Y: 10}}) {
		t.Error("a move onto text with a tooltip doesn't ask for a frame")
	}
	frame()
	clk.advance(time.Second)
	if !strings.Contains(frame(), "14:03:12") {
		t.Error("no tooltip over the text")
	}
	u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 10, Y: 10}, Buttons: render.ButtonPrimary})
	frame()
	u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: 10, Y: 10}, Buttons: render.ButtonPrimary})
	frame()
	if !row.Clicked {
		t.Error("a click on the text with a tooltip wasn't the row's")
	}
}

// A drag measures from where the press went down: on a busy machine the pointer can be well along before the first
// frame built after the press, and a drag that started from there would come up short.
func TestPressedAtIsWhereThePressWentDown(t *testing.T) {
	h := newHarness()
	at := func() (render.Point, bool, bool) {
		var in Interaction
		paintOps(h.u, render.Point{X: 300, Y: 100}, func(c *Ctx) {
			c.Row("bar", Style{Pad: Pad(10), Gap: 10}, func(c *Ctx) { in = button(c, "save", false) })
		})
		p, ok := in.PressedAt()
		return p, ok, in.Held
	}
	if _, ok, _ := at(); ok {
		t.Error("pressed at somewhere before any press")
	}
	h.pointer(render.PointerMove, 20, 20)
	h.frame()
	h.pointer(render.PointerPress, 20, 20)
	h.pointer(render.PointerMove, 40, 22) // no frame between the press and the moves
	h.pointer(render.PointerMove, 60, 24)
	if p, ok, held := at(); !ok || !held || p != (render.Point{X: 20, Y: 20}) {
		t.Errorf("held, pressed at %v %v (held %v), want {20 20}", p, ok, held)
	}
	h.pointer(render.PointerRelease, 60, 24)
	if p, ok, _ := at(); !ok || p != (render.Point{X: 20, Y: 20}) {
		t.Errorf("released, pressed at %v %v, want {20 20}", p, ok)
	}
}

// The wheel over a list with nothing to scroll leaves it where it is: the next frame is built at the top, with every
// row and the highlight where they were, not as if scrolled for a frame until layout puts it back.
func TestWheelOverWhatCantScroll(t *testing.T) {
	u := newUI()
	var list ID
	var built []float32
	build := func(c *Ctx) {
		list = c.ListBehind("list", Style{W: Px(200), H: Px(300)}, 5, 22, func(lc *Ctx, top float32) { built = append(built, top) },
			func(rc *Ctx, i int) { rc.Box("row", Style{W: Flex(1), H: Flex(1)}, nil) })
	}
	paintOps(u, render.Point{X: 300, Y: 400}, build)
	paintOps(u, render.Point{X: 300, Y: 400}, build)
	before := built[len(built)-1]
	u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 50}, Scroll: render.Point{Y: 120}})
	if got := u.Scroll(list); got != (render.Point{}) {
		t.Errorf("the wheel moved a list with nothing to scroll to %v", got)
	}
	paintOps(u, render.Point{X: 300, Y: 400}, build)
	if got := built[len(built)-1]; got != before {
		t.Errorf("the frame after the wheel was built with its rows from %v, not %v", got, before)
	}
	if st := u.state(list); !st.scrolledAt.IsZero() {
		t.Error("the wheel brought up the scrollbar of a list with nothing to scroll")
	}

	// One that scrolls stops at its end, as it's asked.
	u2 := newUI()
	var tall ID
	build2 := func(c *Ctx) {
		tall = c.ListBehind("list", Style{W: Px(200), H: Px(100)}, 10, 22, nil, func(rc *Ctx, i int) { rc.Box("row", Style{W: Flex(1), H: Flex(1)}, nil) })
	}
	paintOps(u2, render.Point{X: 300, Y: 400}, build2)
	u2.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 50, Y: 50}, Scroll: render.Point{Y: 1000}})
	if got := u2.Scroll(tall); got != (render.Point{Y: 120}) {
		t.Errorf("scrolled to %v, want its end at 120", got)
	}
}

// A tooltip can be drawn by a view of its own, for more than a line of text: it comes up after the same delay, over
// everything, and goes when the pointer does.
func TestATooltipCanBeAView(t *testing.T) {
	clk := &clock{t: time.Unix(1000, 0)}
	u := newUI()
	u.SetClock(clk.now)
	frame := func() string {
		return describe(paintOps(u, render.Point{X: 400, Y: 200}, func(c *Ctx) {
			c.Box("pr", Style{W: Px(200), H: Px(30), Tooltip: "acme/api#812", TooltipView: func(c *Ctx) {
				c.Column("tip", Style{Gap: 4}, func(c *Ctx) {
					c.Text("key", "acme/api#812", body)
					c.Text("who", "by mkhan · asked: you, ana", body)
				})
			}}, func(c *Ctx) { c.Text("t", "Rate-limit…", body) })
		}))
	}
	frame()
	u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 10, Y: 10}})
	frame()
	if strings.Contains(frame(), "by mkhan") {
		t.Error("the view before the tooltip's delay")
	}
	clk.advance(time.Second)
	got := frame()
	if !strings.Contains(got, "by mkhan · asked: you, ana") || !strings.Contains(got, "acme/api#812") {
		t.Errorf("no view for the tooltip:\n%s", got)
	}
	u.Pointer(render.PointerEvent{Kind: render.PointerLeave})
	clk.advance(time.Second)
	if strings.Contains(frame(), "by mkhan") {
		t.Error("the tooltip stayed once the pointer left")
	}
}
