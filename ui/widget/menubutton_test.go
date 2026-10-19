package widget

import (
	"slices"
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A ToggleMenu opens under its button, right-aligned with it; choosing an item flips it and leaves the menu open for
// the next, and Escape closes it.
func TestToggleMenu(t *testing.T) {
	a, b := true, false
	var btn ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) {
			btn = c.Next(struct{ k any }{"cog"})
			ToggleMenu(c, "cog", []ToolItem{{Key: "a", Label: "status", On: &a}, {Key: "b", Label: "hidden files", On: &b}},
				MenuButtonOpts{Label: "View options", Glyph: "⚙", Help: "test/menu"})
		})
	})
	h.frame()
	if slices.Contains(h.texts(), "hidden files") {
		t.Fatal("the menu shows before the button is clicked")
	}
	h.click(mid(h.rect(btn)))
	h.frame()
	item := h.find("hidden files")
	if item == nil {
		t.Fatalf("clicking the button didn't open the menu: %q", h.texts())
	}
	if r, br := item.Bounds, h.rect(btn); r.Max.X > br.Max.X+1 || r.Min.Y < br.Max.Y {
		t.Errorf("the menu at %v isn't under its button %v, right-aligned", r, br)
	}
	h.click(mid(item.Bounds))
	if !b {
		t.Fatal("choosing hidden files didn't turn it on")
	}
	h.frame()
	if h.find("status") == nil {
		t.Fatal("the menu closed after a checkable item was chosen")
	}
	h.click(mid(h.find("status").Bounds))
	if a {
		t.Error("choosing status didn't turn it off")
	}
	h.keys("Esc")
	h.frame()
	if h.find("hidden files") != nil {
		t.Error("Escape didn't close the menu")
	}
}
