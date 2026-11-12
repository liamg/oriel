package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// SelectRow is a list's row a click selects, as List's are: a list item that tints under the pointer instead of a
// button's wash, filled and edged in the selection's colours while selected, its own fill otherwise; a double click
// is reported, to open it.
func TestSelectRow(t *testing.T) {
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		sel := false
		var id ui.ID
		var in ui.Interaction
		own := render.Color{R: 40, G: 160, B: 60, A: 30}
		h := newHarness(t, v, small, func(c *ui.Ctx) {
			id = c.Next("row")
			in = SelectRow(c, "row", ui.Style{W: ui.Flex(1), H: ui.Px(20), Fill: own, Label: "a line", Help: "test/row"}, sel, func(c *ui.Ctx) {
				c.Text("t", "a line", ui.TextStyle{})
			})
			if in.Clicked {
				sel = true
			}
		})
		r := h.rect(id)
		fills := func() map[render.Color]bool {
			out := map[render.Color]bool{}
			for _, op := range h.list.Ops {
				if op.Kind == render.OpRect && op.Style.Fill.A > 0 && op.Rect.Min.Y >= r.Min.Y-.5 && op.Rect.Max.Y <= r.Max.Y+.5 {
					out[op.Style.Fill] = true
				}
			}
			return out
		}
		th := resolved(t, v)
		if n := h.access(id); n == nil || n.Role != ui.RoleListItem || n.Selected {
			t.Fatalf("%v: the row's node %+v, want an unselected list item", v, n)
		}
		if f := fills(); !f[own] || f[th.C(tRowPicked)] {
			t.Errorf("%v: unselected, its fills are %v, want its own", v, f)
		}

		h.pointer(render.PointerMove, 0, r.Min.X+30, r.Min.Y+10)
		h.frame()
		if f := fills(); !f[th.C(tRowHover)] || !f[own] {
			t.Errorf("%v: under the pointer, its fills are %v, want the row hover's over its own", v, f)
		}
		if h.u.Options().Hover.A > 0 && h.list.Ops != nil {
			for _, op := range h.list.Ops {
				if op.Kind == render.OpRect && op.Style.Fill == h.u.Options().Hover {
					t.Errorf("%v: the row has a button's hover wash", v)
				}
			}
		}

		h.click(r.Min.X+30, r.Min.Y+10)
		h.frame()
		if n := h.access(id); n == nil || !n.Selected {
			t.Errorf("%v: a click didn't select the row", v)
		}
		if f := fills(); !f[th.C(tRowPicked)] || !f[th.C(tRowEdge)] {
			t.Errorf("%v: selected, its fills are %v, want the selection's fill and edge", v, f)
		}

		double := false
		h.build = func(c *ui.Ctx) {
			in = SelectRow(c, "row", ui.Style{W: ui.Flex(1), H: ui.Px(20), Fill: own, Label: "a line", Help: "test/row"}, sel, func(c *ui.Ctx) {
				c.Text("t", "a line", ui.TextStyle{})
			})
			double = double || in.DoubleClicked
		}
		h.click(r.Min.X+30, r.Min.Y+10)
		h.click(r.Min.X+30, r.Min.Y+10)
		if !double {
			t.Errorf("%v: two quick clicks weren't a double click", v)
		}
	}
}
