package widget

import (
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A dialog's first field (DefaultFocus) takes the keys as the dialog opens, without a click; ⏎ submits it and Esc
// cancels it.
func TestDialogField(t *testing.T) {
	text := ""
	submitted, cancelled := 0, 0
	open := true
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Modal(c, "dialog", &open, ModalOpts{Title: "Name it", Width: 320, Help: "test/input"}, func(c *ui.Ctx) {
			DialogField(c, "name", &text, InputOpts{Label: "Name", Help: "test/input", DefaultFocus: true},
				func() { cancelled++ }, func() { submitted++ })
		})
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
