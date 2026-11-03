package widget

import (
	"cmp"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

type MenuButtonOpts struct {
	Label string // its accessible name, and its tooltip
	Glyph string // what the button shows, when Icon is nil
	Icon  func(c *ui.Ctx, fg render.Color)
	Size  float32 // the button's side; 20 if 0
	Width float32 // the menu's; 220 if 0
	Help  string
	// Hidden keeps the button's room but doesn't draw it, unless its menu is open: a "⋯" that shows on hover.
	Hidden bool
}

type menuButtonState struct{ open bool }

// MenuButton is a small button that opens items as a menu under it, right-aligned with it. Choosing a Checkable item
// leaves the menu open, so several can be ticked in turn; Escape or a click outside closes it. It returns the item
// chosen this frame, or -1.
func MenuButton(c *ui.Ctx, key any, items []MenuItem, o MenuButtonOpts) int {
	id := c.Next(key)
	st := ui.State[menuButtonState](c, id)
	btn := c.Next(struct{ k any }{key})
	if o.Hidden && !st.open {
		side := cmp.Or(o.Size, 20)
		c.Box(struct{ k any }{key}, ui.Style{W: ui.Px(side), H: ui.Px(side)}, nil)
		return -1
	}
	if IconButton(c, struct{ k any }{key}, o.Glyph, IconButtonOpts{Label: o.Label, Help: o.Help, Size: o.Size, Icon: o.Icon, On: st.open}) {
		st.open = !st.open
		if st.open {
			c.Focus(ui.Child(id, "menu"))
		}
	}
	if !st.open {
		return -1
	}
	if catcher(c, struct{ k, c any }{key, "catch"}, ui.LayerPopup, o.Help) {
		st.open = false
		return -1
	}
	w := o.Width
	if w == 0 {
		w = 220
	}
	chosen, closed := -1, false
	c.Box(key, ui.Style{Z: ui.LayerPopup, Float: &ui.Float{To: btn, At: render.Point{X: 1, Y: 1}, Self: render.Point{X: 1}, Offset: render.Point{Y: 4}}},
		func(c *ui.Ctx) {
			chosen, closed = Menu(c, "menu", items, MenuOpts{Width: w, Help: o.Help})
		})
	if closed || chosen >= 0 && !items[chosen].Checkable {
		st.open = false
	}
	return chosen
}

// ToggleMenu is a MenuButton of on/off items, each ticked while it's on; choosing one flips it. It reports whether
// one changed.
func ToggleMenu(c *ui.Ctx, key any, items []ToolItem, o MenuButtonOpts) bool {
	menu := make([]MenuItem, len(items))
	for i, it := range items {
		menu[i] = MenuItem{Label: it.Label, Shortcut: it.Shortcut, Help: it.Help, Checkable: true, Checked: *it.On}
	}
	i := MenuButton(c, key, menu, o)
	if i < 0 {
		return false
	}
	*items[i].On = !*items[i].On
	return true
}
