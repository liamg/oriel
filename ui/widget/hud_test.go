package widget

import (
	"testing"

	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// An indicator reads as one status: its label, its share, its value; past Warn it's in the warning colour.
func TestIndicator(t *testing.T) {
	var a, b ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{}, func(c *ui.Ctx) {
			a = Indicator(c, "ctx", IndicatorOpts{Label: "CTX", Meter: true, Frac: .38, Help: "test/ind"})
			b = Indicator(c, "deploy", IndicatorOpts{Label: "DEPLOY", Value: "4", Dot: col(c, theme.Warn)})
		})
	})
	h.frame()
	if n := h.access(a); n == nil || n.Name != "CTX 38%" {
		t.Errorf("the meter's indicator: %+v", n)
	}
	if n := h.access(b); n == nil || n.Name != "DEPLOY 4" {
		t.Errorf("the value's indicator: %+v", n)
	}
	for _, s := range []string{"CTX", "38%", "DEPLOY", "4"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
}

// A panel heads its body with its title in caps and its note, and is as tall as its parent gives it.
func TestPanel(t *testing.T) {
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("row", ui.Style{W: ui.Px(400), H: ui.Px(200)}, func(c *ui.Ctx) {
			id = Panel(c, "p", PanelOpts{Title: "Workers", Note: "3 online", Help: "test/panel"}, func(c *ui.Ctx) {
				c.Text("t", "mbp", ui.TextStyle{})
			})
		})
	})
	h.frame()
	for _, s := range []string{"WORKERS", "3 online", "mbp"} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
	if r := h.rect(id); r.Dx() != 400 || r.Dy() != 200 {
		t.Errorf("the panel's rect: %v", r)
	}
}

// A drawer hangs from the bottom of the window's top area, across the window; a press anywhere in it is reported, and so is a click on its grab. A
// pinned one takes focus, so its keys work, and gives it back to nothing in particular when it goes.
func TestDrawer(t *testing.T) {
	pinned := false
	var id, inner, grab ui.ID
	var res DrawerResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Area(c, AreaTop, "top", ui.Style{W: ui.Flex(1), H: ui.Px(38)}, nil)
		id = c.Next("hud")
		res = Drawer(c, "hud", DrawerOpts{Pinned: pinned, Grab: "CLICK TO PIN", Label: "HUD", Help: "test/hud", KeyMode: "hud"}, func(c *ui.Ctx) {
			inner = c.Next("b")
			Clickable(c, "b", ui.Style{W: ui.Px(60), H: ui.Px(20), Help: "test/hud"}, nil)
			c.Box("body", ui.Style{W: ui.Flex(1), H: ui.Px(200)}, nil)
		})
		grab = ui.Child(id, "grab")
	})
	h.frame()
	h.frame()
	r := h.rect(id)
	if r.Min.Y != 38 || r.Dx() != small.X {
		t.Fatalf("the drawer is at %v", r)
	}
	if g := h.rect(grab); g.Min.Y < r.Max.Y-1 {
		t.Errorf("the grab %v isn't under the drawer's edge %v", g, r)
	}
	pressed, grabbed := false, false
	h.build = func(c *ui.Ctx) {
		Area(c, AreaTop, "top", ui.Style{W: ui.Flex(1), H: ui.Px(38)}, nil)
		id = c.Next("hud")
		res = Drawer(c, "hud", DrawerOpts{Pinned: pinned, Grab: "CLICK TO PIN", Help: "test/hud", KeyMode: "hud"}, func(c *ui.Ctx) {
			inner = c.Next("b")
			Clickable(c, "b", ui.Style{W: ui.Px(60), H: ui.Px(20), Help: "test/hud"}, nil)
			c.Box("body", ui.Style{W: ui.Flex(1), H: ui.Px(200)}, nil)
		})
		grab = ui.Child(id, "grab")
		pressed, grabbed = pressed || res.Pressed, grabbed || res.Grabbed
	}
	h.click(mid(h.rect(inner)))
	if !pressed {
		t.Error("a click on a button in the drawer isn't a press within it")
	}
	h.click(mid(h.rect(grab)))
	if !grabbed {
		t.Error("a click on the grab wasn't reported")
	}
	pinned = true
	h.frame()
	h.frame()
	if f := h.u.Focused(); f != id {
		t.Errorf("a pinned drawer's focus: %v, want %v", f, id)
	}
	if h.u.KeyMode() != "hud" {
		t.Errorf("its key mode: %q", h.u.KeyMode())
	}
}
