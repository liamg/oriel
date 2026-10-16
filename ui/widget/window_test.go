package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// drag presses at from, moves to to in two steps and lets go, a frame between each.
func (h *harness) drag(from, to render.Point) {
	h.pointer(render.PointerMove, 0, from.X, from.Y)
	h.frame()
	h.pointer(render.PointerPress, render.ButtonPrimary, from.X, from.Y)
	h.frame()
	half := render.Point{X: (from.X + to.X) / 2, Y: (from.Y + to.Y) / 2}
	h.pointer(render.PointerMove, render.ButtonPrimary, half.X, half.Y)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, to.X, to.Y)
	h.frame()
	h.pointer(render.PointerRelease, render.ButtonPrimary, to.X, to.Y)
	h.frame()
}

func windowScene(t *testing.T, st *WindowState, res *WindowResult) *harness {
	return newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Box("under", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, nil)
		*res = Window(c, "w", st, WindowOpts{Title: "ORI", Default: render.Rect{Min: render.Point{X: 100, Y: 100}, Max: render.Point{X: 400, Y: 300}},
			Min: render.Point{X: 200, Y: 120}, Minimise: true, Help: "test/window"}, func(c *ui.Ctx) {
			c.Text("t", "hello", ui.TextStyle{})
		})
	})
}

// A window opens at its default place, with its title and body; dragging its title bar moves it, and it can't be
// dragged out of the view.
func TestWindowMoves(t *testing.T) {
	st, res := &WindowState{}, WindowResult{}
	h := windowScene(t, st, &res)
	h.frame()
	if r := h.rect(res.ID); r.Min != (render.Point{X: 100, Y: 100}) || r.Dx() != 300 || r.Dy() != 200 {
		t.Fatalf("the window opened at %v", r)
	}
	for _, s := range []string{"ORI", "hello"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
	h.drag(render.Point{X: 200, Y: 110}, render.Point{X: 260, Y: 150})
	if st.Rect.Min != (render.Point{X: 160, Y: 140}) || st.Rect.Dx() != 300 {
		t.Errorf("dragged by its title, the window is at %v", st.Rect)
	}
	if r := h.rect(res.ID); r.Min != st.Rect.Min {
		t.Errorf("it's drawn at %v, kept at %v", r, st.Rect)
	}
	if st.Dragging() {
		t.Error("still dragging after the button came up")
	}
	h.drag(render.Point{X: 260, Y: 150}, render.Point{X: 900, Y: 900})
	if st.Rect.Max.X != small.X || st.Rect.Max.Y != small.Y || st.Rect.Dx() != 300 {
		t.Errorf("dragged past the corner, the window is at %v", st.Rect)
	}
}

// Its bottom-right corner resizes it, down to its Min and no further; its left edge moves its left side only.
func TestWindowResizes(t *testing.T) {
	st, res := &WindowState{}, WindowResult{}
	h := windowScene(t, st, &res)
	h.frame()
	h.drag(render.Point{X: 397, Y: 297}, render.Point{X: 457, Y: 337})
	if st.Rect != (render.Rect{Min: render.Point{X: 100, Y: 100}, Max: render.Point{X: 460, Y: 340}}) {
		t.Errorf("after its corner was dragged out, the window is %v", st.Rect)
	}
	h.drag(render.Point{X: 457, Y: 337}, render.Point{X: 110, Y: 110})
	if st.Rect.Dx() != 200 || st.Rect.Dy() != 120 || st.Rect.Min != (render.Point{X: 100, Y: 100}) {
		t.Errorf("dragged in past its Min, the window is %v", st.Rect)
	}
	h.drag(render.Point{X: 297, Y: 217}, render.Point{X: 900, Y: 900})
	if st.Rect != (render.Rect{Min: render.Point{X: 100, Y: 100}, Max: small}) {
		t.Errorf("its corner dragged off the view, the window is %v: it stops at the edge, and doesn't move", st.Rect)
	}
	h.drag(render.Point{X: 102, Y: 160}, render.Point{X: 52, Y: 160})
	if st.Rect.Min.X != 50 || st.Rect.Max.X != small.X {
		t.Errorf("after its left edge was dragged, the window is %v", st.Rect)
	}
}

// Its minimise button reports a click, and the window reads as one dialog named by its title.
func TestWindowMinimise(t *testing.T) {
	st, res := &WindowState{}, WindowResult{}
	minimised := false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		res = Window(c, "w", st, WindowOpts{Title: "ORI", Default: render.Rect{Min: render.Point{X: 100, Y: 100}, Max: render.Point{X: 400, Y: 300}},
			Minimise: true, Help: "test/window"}, nil)
		minimised = minimised || res.Minimised
	})
	h.frame()
	min := h.rect(ui.Child(ui.Child(res.ID, windowPartKey{partHead}), "min"))
	h.click(mid(min))
	if !minimised {
		t.Error("clicking – didn't minimise it")
	}
	if n := h.access(res.ID); n == nil || n.Name != "ORI" {
		t.Errorf("the window's accessible node: %+v", n)
	}
}

func TestBadge(t *testing.T) {
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("r", ui.Style{Gap: 4}, func(c *ui.Ctx) {
			Badge(c, "none", 0, BadgeOpts{})
			Badge(c, "three", 3, BadgeOpts{})
			Badge(c, "lots", 140, BadgeOpts{})
		})
	})
	h.frame()
	for _, s := range []string{"3", "99+"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
	if h.drew("0") {
		t.Error("drew a badge for nothing")
	}
}
