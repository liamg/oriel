package widget

import (
	"fmt"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A block asked to be revealed scrolls into view once, and the ask is spent.
func TestScrolledReveals(t *testing.T) {
	st := &Scroll{}
	var view, last ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 300, Y: 200}, func(c *ui.Ctx) {
		view = c.Next("view")
		Scrolled(c, "view", st, ui.Style{W: ui.Px(300), H: ui.Px(200)}, ui.Style{}, func(c *ui.Ctx, block func(string) ui.ID) {
			for i := range 40 {
				k := fmt.Sprintf("b%d", i)
				id := block(k)
				if i == 39 {
					last = id
				}
				c.Box(k, ui.Style{H: ui.Px(30)}, nil)
			}
		})
	})
	h.frame()
	if r, v := h.rect(last), h.rect(view); r.Min.Y < v.Max.Y {
		t.Fatalf("the last block starts in view (%v in %v) before it's revealed", r, v)
	}
	st.Reveal = "b39"
	h.frame()
	h.frame()
	if r, v := h.rect(last), h.rect(view); r.Min.Y < v.Min.Y || r.Max.Y > v.Max.Y {
		t.Errorf("the revealed block is at %v, outside the view %v", r, v)
	}
	if st.Reveal != "" {
		t.Errorf("the reveal is still asked for: %q", st.Reveal)
	}
}
