package widget

import (
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A dialog's field takes the keys as the dialog opens, without a click; ⏎ submits it and Esc cancels it.
func TestDialogField(t *testing.T) {
	text := ""
	open := true
	submitted, cancelled := 0, 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			DialogField(c, "name", &text, InputOpts{Label: "Name", Help: "test/input"}, open,
				func() { cancelled++ }, func() { submitted++ })
		})
		open = false
	})
	h.frame()
	h.keys("a", "b")
	if text != "ab" {
		t.Fatalf("typed into the field as it opened: %q", text)
	}
	h.keys("CR")
	if submitted != 1 || cancelled != 0 {
		t.Errorf("⏎: submitted %d, cancelled %d", submitted, cancelled)
	}
	h.keys("Esc")
	if cancelled != 1 {
		t.Errorf("Esc: cancelled %d", cancelled)
	}
}
