package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A hidden IconButton keeps its room, so what's beside it doesn't move when it shows: a tab's ✕ shown on hover. It
// draws nothing, has no node for assistive technology, and a click there does nothing.
func TestAHiddenIconButtonKeepsItsRoom(t *testing.T) {
	hidden := true
	clicked := false
	var after ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("row", ui.Style{}, func(c *ui.Ctx) {
			if IconButton(c, "x", "✕", IconButtonOpts{Label: "Close it", Size: 16, Hidden: hidden}) {
				clicked = true
			}
			after = c.Text("after", "next", ui.TextStyle{})
		})
	})
	hiddenAt := h.rect(after)
	if n := h.find("Close it"); n != nil {
		t.Error("a hidden button has an accessible node")
	}
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Rect.Dx() > 0 && op.Rect.Max.X <= hiddenAt.Min.X {
			t.Error("a hidden button draws its glyph")
		}
	}
	h.click(hiddenAt.Min.X-8, (hiddenAt.Min.Y+hiddenAt.Max.Y)/2)
	if clicked {
		t.Error("a click on a hidden button clicked it")
	}

	hidden = false
	h.frame()
	if shownAt := h.rect(after); shownAt != hiddenAt {
		t.Errorf("shown, what's after it moved from %v to %v", hiddenAt, shownAt)
	}
	if h.find("Close it") == nil {
		t.Error("shown, the button has no node")
	}
}
