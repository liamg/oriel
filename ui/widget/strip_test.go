package widget

import (
	"fmt"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// texts is every run of text a frame draws.
func texts(l *render.List) string {
	var b strings.Builder
	for _, op := range l.Ops {
		if op.Kind == render.OpGlyphs {
			b.WriteString(op.Run.Text + "\n")
		}
	}
	return b.String()
}

// A Strip of tabs too many to fit: the active one is brought into view, the wheel (up and down too) scrolls it
// away and it stays there, each edge counts the tabs past it with no scrollbar drawn, and a click on a count brings
// the next of them into view.
func TestStripScrollsFreelyAndCountsWhatsPastItsEdges(t *testing.T) {
	active := 19
	var items []any
	for i := range 20 {
		items = append(items, i)
	}
	var id ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 300, Y: 40}, func(c *ui.Ctx) {
		id = Strip(c, "strip", ui.Style{W: ui.Flex(1), H: ui.Px(28)}, StripOpts{Items: items, Active: active}, func(c *ui.Ctx) {
			for i := range 20 {
				c.Row(i, ui.Style{W: ui.Px(60), H: ui.Flex(1), Keep: true}, func(c *ui.Ctx) { c.Text("t", fmt.Sprintf("tab%d", i), ui.TextStyle{}) })
			}
		})
	})
	for range 3 {
		h.frame()
	}
	if s := h.u.Scroll(id).X; s != 20*60-300 {
		t.Fatalf("the active tab at the end: scrolled to %v, want %v", s, 20*60-300)
	}
	if s := texts(h.list); !strings.Contains(s, "15 ›") && !strings.Contains(s, "‹ 15") {
		t.Errorf("no count of the 15 tabs past the left edge:\n%s", s)
	}
	h.u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: 150, Y: 14}, Scroll: render.Point{Y: -1e4}})
	for range 3 {
		h.frame()
	}
	if s := h.u.Scroll(id).X; s != 0 {
		t.Fatalf("the wheel up scrolled it to %v, and it came back; want it left at the start", s)
	}
	out := texts(h.list)
	if !strings.Contains(out, "15 ›") || strings.Contains(out, "‹") {
		t.Errorf("at the start, want only a count of 15 past the right edge:\n%s", out)
	}
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && op.Rect.Max.Y > 28-1 && op.Rect.Dy() <= 9 && op.Rect.Min.Y > 14 {
			t.Errorf("a scrollbar is drawn: %+v", op.Rect)
		}
	}
	h.click(300-10, 14)
	h.frame()
	if s := h.u.Scroll(id).X; s <= 0 {
		t.Errorf("a click on the count didn't scroll: %v", s)
	}
}
