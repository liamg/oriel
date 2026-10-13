package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func TestSelectInAPage(t *testing.T) {
	value, open := "dark", true
	var drop ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Page(c, "page", &open, PageOpts{KeyMode: "page", Help: "test/page"}, func(c *ui.Ctx) {
			drop = ui.Child(c.Next("variant"), "drop")
			if v, ok := Select(c, "variant", value, []string{"dark", "light", "system"}, SelectOpts{Label: "Variant", Help: "test/select"}); ok {
				value = v
			}
		})
	})
	x, y := mid(h.rect(drop))
	h.click(x, y)
	// The menu's third row, under the button: it paints over the page, so the pointer finds it.
	h.click(x, h.rect(drop).Max.Y+4+12+4+30*2+15)
	if value != "system" {
		t.Fatalf("picking the third item: value %q, want system", value)
	}
}

func TestInputShowsNextFrame(t *testing.T) {
	on := false
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("t")
		if Toggle(c, "t", on, ToggleOpts{Label: "On", Help: "test/toggle"}) {
			on = !on
		}
	})
	x, y := mid(h.rect(id))
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.pointer(render.PointerPress, render.ButtonPrimary, x, y)
	h.frame()
	h.pointer(render.PointerRelease, render.ButtonPrimary, x, y)
	h.frame()
	if !on {
		t.Fatal("a click didn't flip the toggle")
	}
	if next := h.u.NextFrame(); next.IsZero() || next.After(h.now) {
		t.Errorf("the frame that read the click asks for the next at %v, want now: the flip shows a frame late", next)
	}
	h.frame()
	if next := h.u.NextFrame(); !next.IsZero() {
		t.Errorf("with no input, a frame still asks for another at %v", next)
	}
}

func TestEscapeBubblesFromAToggle(t *testing.T) {
	on, open := false, true
	var tog ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Page(c, "page", &open, PageOpts{KeyMode: "page", Help: "test/page"}, func(c *ui.Ctx) {
			tog = c.Next("t")
			if Toggle(c, "t", on, ToggleOpts{Label: "On", Help: "test/toggle"}) {
				on = !on
			}
		})
	})
	h.click(mid(h.rect(tog)))
	if h.u.Focused() != tog {
		t.Fatal("a click didn't focus the toggle")
	}
	h.keys(" ")
	if on || !open {
		t.Fatalf("Space flips the toggle back and leaves the page open: on %v, open %v", on, open)
	}
	h.keys("Esc")
	h.frame()
	if open {
		t.Error("Escape on a focused toggle didn't reach the page and close it")
	}
}

func TestFocusFromUnderADialog(t *testing.T) {
	var behind, front ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		size := c.Size()
		c.Box("dialog", ui.Style{Z: ui.LayerDialog, Float: &ui.Float{Point: &render.Point{}}}, func(c *ui.Ctx) {
			front = c.Next("in")
			c.Box("in", ui.Style{W: ui.Px(100), H: ui.Px(20), Focusable: true, KeyMode: "input", Help: "test/focus"}, nil)
			if h := c.Interaction(front); !h.Focused {
				c.Focus(front)
			}
		})
		// A page built later, under the dialog, that takes focus whenever it doesn't have it.
		c.Box("page", ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Z: ui.LayerPage, Float: &ui.Float{Point: &render.Point{}}}, func(c *ui.Ctx) {
			behind = c.Next("p")
			c.Box("p", ui.Style{W: ui.Px(100), H: ui.Px(20), Focusable: true, KeyMode: "page", Help: "test/focus"}, nil)
			c.Focus(behind)
		})
	})
	for i := range 3 {
		h.frame()
		if f := h.u.Focused(); f != front {
			t.Errorf("frame %d: focus is on %v, want the dialog's %v, not the page's %v under it", i, f, front, behind)
		}
	}
}

func TestScrim(t *testing.T) {
	open := true
	var dialog ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		if !open {
			return
		}
		if Scrim(c, "scrim", ScrimOpts{Z: ui.LayerDialog, Help: "test/scrim"}, func(c *ui.Ctx) {
			dialog = c.Next("dialog")
			c.Box("dialog", ui.Style{W: ui.Px(200), H: ui.Px(100), Interactive: true, Float: &ui.Float{Point: &render.Point{X: 100, Y: 100}}}, nil)
		}) {
			open = false
		}
	})
	h.click(mid(h.rect(dialog)))
	if !open {
		t.Fatal("a click on the dialog closed it")
	}
	h.click(20, 20)
	if open {
		t.Error("a click on the scrim didn't close the dialog")
	}
}
