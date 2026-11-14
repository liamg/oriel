package widget

import (
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A disabled button says why it can't be pressed once the pointer rests on it, and a click on it does nothing.
func TestADisabledButtonSaysWhy(t *testing.T) {
	var clicked bool
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10)}, func(c *ui.Ctx) {
			clicked = Button(c, "push", "Push", ButtonOpts{Disabled: true, Why: "Nothing to push: origin has every commit"}) || clicked
		})
	})
	h.pointer(render.PointerMove, 0, 20, 20)
	h.frame()
	h.clock.Advance(2 * time.Second)
	h.frame()
	if !strings.Contains(strings.Join(h.texts(), "|"), "Nothing to push") {
		t.Errorf("no tooltip saying why: %q", h.texts())
	}
	h.click(20, 20)
	if clicked {
		t.Error("a disabled button was clicked")
	}
}
