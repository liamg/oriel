package panes

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// fieldsHarness is the layout with a field in every pane, its default target, and one outside the layout.
type fieldsHarness struct {
	*harness
	fields  map[layout.ViewRef]ui.ID
	outside ui.ID
}

func newFieldsHarness(t *testing.T) *fieldsHarness {
	h := &fieldsHarness{harness: newHarness(t), fields: map[layout.ViewRef]ui.ID{}}
	h.frame()
	h.frame()
	return h
}

func (h *fieldsHarness) frame() {
	h.list = new(render.List)
	h.u.Frame(h.list.Record(1), screen, func(c *ui.Ctx) {
		c.Column("window", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			h.outside = c.Box("search", ui.Style{W: ui.Px(200), H: ui.Px(20), Focusable: true}, nil)
			View(c, "layout", h.r, &h.panes, func(c *ui.Ctx, v layout.ViewRef, focused bool) {
				h.fields[v] = c.Box("field", ui.Style{W: ui.Px(100), H: ui.Px(20), Focusable: true, DefaultFocus: true}, nil)
			})
		})
	})
}

func (h *fieldsHarness) click(id ui.ID) {
	r, _ := h.u.Rect(id)
	p := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
	for _, k := range []render.PointerKind{render.PointerMove, render.PointerPress, render.PointerRelease} {
		h.u.Pointer(render.PointerEvent{Kind: k, Pos: p, Buttons: render.ButtonPrimary})
		h.frame()
	}
}

// The focused view is the one whose pane holds keyboard focus; one focused by a command takes keyboard focus once.
func TestFocusedViewFollowsKeyboardFocus(t *testing.T) {
	h := newFieldsHarness(t)
	if f := h.u.Focused(); f != h.fields[code] {
		t.Fatalf("at the start focus is on %v, want the focused view's field %v", f, h.fields[code])
	}
	h.click(h.fields[term])
	if *h.r.Focus != term {
		t.Errorf("a click into the terminal's field left %s focused", h.r.Focus)
	}
	h.click(h.outside)
	for range 5 {
		h.frame()
	}
	if f := h.u.Focused(); f != h.outside {
		t.Errorf("focus left the field outside the layout for %v", f)
	}
	if *h.r.Focus != term {
		t.Errorf("focus outside the layout changed the focused view to %s", h.r.Focus)
	}
	h.r.FocusDir(layout.DirUp, h.panes.Rects(h.u))
	h.frame()
	if f, want := h.u.Focused(), h.fields[*h.r.Focus]; f != want {
		t.Errorf("⌃W k to %s: focus on %v, want its field %v", h.r.Focus, f, want)
	}
}

// A press on a pane where nothing takes focus focuses the pane's default target.
func TestPressOnAPaneFocusesItsDefault(t *testing.T) {
	h := newFieldsHarness(t)
	h.click(h.outside)
	r, _ := h.u.Rect(h.fields[code])
	p := render.Point{X: r.Min.X + 10, Y: r.Max.Y + 60}
	for _, k := range []render.PointerKind{render.PointerMove, render.PointerPress, render.PointerRelease} {
		h.u.Pointer(render.PointerEvent{Kind: k, Pos: p, Buttons: render.ButtonPrimary})
		h.frame()
	}
	if f := h.u.Focused(); f != h.fields[code] {
		t.Errorf("a press in the editor's pane left focus on %v, want its field %v", f, h.fields[code])
	}
}
