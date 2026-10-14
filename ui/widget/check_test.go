package widget

import (
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// A checkbox flips on a click and on Space while focused, shows on, off and mixed, and a disabled one doesn't flip.
func TestCheck(t *testing.T) {
	var id, off ui.ID
	st := CheckMixed
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), Gap: 10}, func(c *ui.Ctx) {
			id = c.Next("c")
			if Check(c, "c", st, CheckOpts{Text: "Mark reviewed", Help: "test/check"}) {
				if st == CheckOn {
					st = CheckOff
				} else {
					st = CheckOn
				}
			}
			off = c.Next("d")
			if Check(c, "d", CheckOff, CheckOpts{Label: "Off", Disabled: true, Help: "test/check"}) {
				t.Error("a disabled checkbox flipped")
			}
		})
	})
	if !h.drew("MARK REVIEWED") {
		t.Errorf("its text: %q", h.texts())
	}
	h.click(mid(h.rect(id)))
	if st != CheckOn {
		t.Fatalf("a click on a mixed box left it %v", st)
	}
	h.frame()
	if n := h.access(id); n == nil || !n.Selected || n.Name != "Mark reviewed" {
		t.Errorf("its accessible state: %+v", n)
	}
	if !h.drew("✓") {
		t.Error("an on box has no tick")
	}
	h.keys(" ")
	if st != CheckOff {
		t.Error("Space didn't turn it off")
	}
	h.click(mid(h.rect(off)))
}
