package widget

import (
	"math"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func tableRows(cells ...[]string) [][][]Span {
	var out [][][]Span
	for _, r := range cells {
		var row [][]Span
		for _, s := range r {
			row = append(row, []Span{{Text: s}})
		}
		out = append(out, row)
	}
	return out
}

// glyphs is the first run drawn with text s.
func (h *harness) glyphs(s string) render.Op {
	h.t.Helper()
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == s {
			return op
		}
	}
	h.t.Fatalf("didn't draw %q: %q", s, h.texts())
	return render.Op{}
}

func nearPx(a, b float32) bool { return math.Abs(float64(a-b)) < 1 }

// A table's columns are as wide as their content when there's room, its header is semibold, and each column's text
// is aligned as it asks.
func TestTableSizesColumnsToContent(t *testing.T) {
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		id = c.Next("t")
		Table(c, "t", tableRows([]string{"Plan", "Seats", "Price"}, []string{"Free", "1", "$0"}, []string{"Team", "10", "$120"}), 600,
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}}, TableOpts{Head: true, Align: []ui.Align{ui.Start, ui.Center, ui.End}, Label: "plans"})
	})
	h.frame()
	if w := h.rect(id).Dx(); w >= 400 {
		t.Errorf("a table of short cells is %v wide in 600", w)
	}
	if h.glyphs("Plan").Run.Font.Weight != typeset.SemiBold || h.glyphs("Free").Run.Font.Weight == typeset.SemiBold {
		t.Error("the header isn't semibold over a plain body")
	}
	right := func(s string) float32 { op := h.glyphs(s); return op.At.X + op.Run.Advance }
	centre := func(s string) float32 { op := h.glyphs(s); return op.At.X + op.Run.Advance/2 }
	if !nearPx(h.glyphs("Plan").At.X, h.glyphs("Team").At.X) {
		t.Error("the first column isn't aligned to its start")
	}
	if !nearPx(centre("Seats"), centre("1")) || !nearPx(centre("1"), centre("10")) {
		t.Errorf("the centred column's centres: %v %v %v", centre("Seats"), centre("1"), centre("10"))
	}
	if !nearPx(right("Price"), right("$0")) || !nearPx(right("$0"), right("$120")) {
		t.Errorf("the right column's right edges: %v %v %v", right("Price"), right("$0"), right("$120"))
	}
	if !(h.glyphs("Plan").At.Y < h.glyphs("Free").At.Y && h.glyphs("Free").At.Y < h.glyphs("Team").At.Y) {
		t.Error("the rows aren't in order down the table")
	}
	if n := h.access(id); n == nil || n.Name != "plans" {
		t.Errorf("the table's accessible node is %+v", n)
	}
}

// A table wider than its room wraps its longest cells to fit, and its text stays in its cells.
func TestTableWrapsToFit(t *testing.T) {
	var id ui.ID
	long := "a cell with enough words in it to need several lines at this width"
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 400}, func(c *ui.Ctx) {
		id = c.Next("t")
		Table(c, "t", tableRows([]string{"Key", "What it does"}, []string{"gd", long}), 300, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}},
			TableOpts{Head: true})
	})
	h.frame()
	if w := h.rect(id).Dx(); w > 300 {
		t.Errorf("the table is %v wide in 300", w)
	}
	ys := map[float32]bool{}
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text != "Key" && op.Run.Text != "What it does" && op.Run.Text != "gd" {
			ys[op.At.Y] = true
		}
	}
	if len(ys) < 2 {
		t.Errorf("the long cell is on %d line(s)", len(ys))
	}
	if o := h.u.Overflowing(); len(o) > 0 {
		t.Errorf("text past its box: %v", o)
	}
}

// A table whose cells can't wrap narrow enough scrolls sideways in its width.
func TestTableScrollsWhenItCantFit(t *testing.T) {
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		id = c.Next("t")
		code := func(s string) []Span { return []Span{{Text: s, Code: true}} }
		Table(c, "t", [][][]Span{{code("registry.BufferPresenter"), code("registry.EditorAction")}, {code("ctx.Block(from, to, draw)"), code("ui.Ctx")}},
			200, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}}, TableOpts{})
	})
	h.frame()
	if w := h.rect(id).Dx(); !nearPx(w, 200) {
		t.Errorf("the scrolling table is %v wide, not its 200", w)
	}
	if op := h.glyphs("registry.EditorAction"); op.At.X+op.Run.Advance <= 200 {
		t.Error("the table's content isn't wider than its box")
	}
	if o := h.u.Overflowing(); len(o) > 0 {
		t.Errorf("text past its box: %v", o)
	}
}

// A table names each row's node as it builds it, so what draws it can tell which row is where.
func TestTableReportsItsRows(t *testing.T) {
	ids := map[int]ui.ID{}
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		Table(c, "t", tableRows([]string{"Plan", "Price"}, []string{"Free", "$0"}, []string{"Team", "$120"}), 600,
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13}}, TableOpts{Head: true, Row: func(r int, id ui.ID) { ids[r] = id }})
	})
	h.frame()
	for r, s := range []string{"Plan", "Free", "Team"} {
		op := h.glyphs(s)
		if rect := h.rect(ids[r]); op.At.Y < rect.Min.Y || op.At.Y > rect.Max.Y || op.At.X < rect.Min.X || op.At.X > rect.Max.X {
			t.Errorf("row %d's node %v doesn't hold %q at %v", r, rect, s, op.At)
		}
	}
}
