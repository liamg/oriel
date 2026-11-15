package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A caption is upper case; a heading spans its row, its note beside it; a keycap is its key in a box wider than it.
func TestTypeHelpers(t *testing.T) {
	var head, key ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 400, Y: 200}, func(c *ui.Ctx) {
		c.Column("col", ui.Style{W: ui.Px(300), Pad: ui.Pad(10), Gap: 8}, func(c *ui.Ctx) {
			head = c.Next("head")
			Heading(c, "head", "Sessions", "3 open")
			Caption(c, "cap", "recent")
			key = c.Next("key")
			Keycap(c, "key", "⌘K")
		})
	})
	for _, want := range []string{"SESSIONS", "3 open", "RECENT", "⌘K"} {
		if !h.drew(want) {
			t.Errorf("drew %q, want %q", h.texts(), want)
		}
	}
	if r := h.rect(head); r.Dx() != 280 {
		t.Errorf("the heading is %v wide, want its row's 280", r.Dx())
	}
	if r := h.rect(key); r.Dx() <= 14 {
		t.Errorf("the keycap is %v wide, want room past its padding", r.Dx())
	}
}
