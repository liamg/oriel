package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type MenuItem struct {
	Label     string
	Shortcut  string
	Help      string
	Disabled  bool
	Separator bool // a line, not an item
}

type MenuOpts struct {
	Width float32 // 250 by default
	Help  string
}

type menuState struct{ hi int }

// Menu is a list of commands (the context menu in 28-help). j/k or Down/Up move the highlight, Enter
// chooses, Escape closes; the pointer highlights and a click chooses. It returns the item chosen this frame,
// or -1, and whether it was closed without a choice.
func Menu(c *ui.Ctx, key any, items []MenuItem, o MenuOpts) (chosen int, closed bool) {
	id := c.Next(key)
	in := c.Interaction(id)
	st := ui.State[menuState](c, id)
	chosen = -1
	usable := func(i int) bool { return i >= 0 && i < len(items) && !items[i].Separator && !items[i].Disabled }
	step := func(d int) {
		for i, n := st.hi+d, 0; n < len(items); i, n = i+d, n+1 {
			i = (i + len(items)) % len(items)
			if usable(i) {
				st.hi = i
				return
			}
		}
	}
	if !usable(st.hi) {
		step(1)
	}
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "j", "Down"):
			step(1)
		case hasKey([]render.KeyEvent{k}, "k", "Up"):
			step(-1)
		case k.Key == "CR" && usable(st.hi):
			chosen = st.hi
		case k.Key == "Esc":
			closed = true
		}
	}
	w := o.Width
	if w == 0 {
		w = 250
	}
	c.Column(key, ui.Style{W: ui.Px(w), Pad: ui.PadXY(0, 4), Fill: col(c, tMenuBg), Stroke: col(c, tMenuBorder), Border: 1, Radius: 6,
		Enter: dropIn, Exit: fade, Focusable: true, KeyMode: "menu", Help: o.Help, Role: ui.RoleMenu}, func(c *ui.Ctx) {
		for i, it := range items {
			if it.Separator {
				c.Box(i, ui.Style{W: ui.Flex(1), H: ui.Px(9), Pad: ui.PadXY(0, 4)}, func(c *ui.Ctx) {
					c.Box("line", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tMenuSeparator)}, nil)
				})
				continue
			}
			ii := c.Interaction(c.Next(i))
			if ii.Hovered && usable(i) {
				st.hi = i
			}
			if ii.Clicked && usable(i) {
				chosen = i
			}
			fill := render.Color{}
			if i == st.hi {
				fill = col(c, tMenuHover)
			}
			fg := col(c, tMenuFg)
			if it.Disabled {
				fg = col(c, tDisabledFg)
			}
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: fill, EaseFill: true, Interactive: true,
				Disabled: it.Disabled, Cursor: render.CursorPointer, Help: it.Help, Role: ui.RoleMenuItem, Label: it.Label}, func(c *ui.Ctx) {
				c.Text("label", it.Label, uiText(c, 12.5, typeset.Normal, fg))
				if it.Shortcut != "" {
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					kbd(c, it.Shortcut)
				}
			})
		}
	})
	return chosen, closed
}

// kbd is a key cap (base.css .kbd).
func kbd(c *ui.Ctx, label string) {
	c.Row("kbd", ui.Style{Pad: ui.PadXY(5, 1), Fill: col(c, tMenuBg), Stroke: col(c, tMenuBorder), Border: 1, Radius: 4}, func(c *ui.Ctx) {
		c.Text(nil, label, monoText(10, typeset.Medium, col(c, tMenuShortcut), 0))
	})
}

// catcher is a transparent float over the whole window, under a popup, that closes it when clicked.
// It shares the popup's help.
func catcher(c *ui.Ctx, key any, z int, help string) bool {
	id := c.Next(key)
	clicked := c.Interaction(id).Clicked
	size := c.Size()
	c.Box(key, ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Z: z, Interactive: true, Help: help, Float: &ui.Float{Point: &render.Point{}}}, nil)
	return clicked
}

// ContextMenuHelp is the context menu's own help page.
const ContextMenuHelp = "ui/context-menu"

type ContextResult struct {
	Chosen int    // the item chosen this frame, or -1
	Help   string // set when "Help" was chosen: the target's help id
	Open   bool   // the menu is showing after this frame
}

type contextState struct {
	open bool
	at   render.Point
}

// ContextMenu opens items where target is right-clicked. If target (or what it's in) has a help id, the
// last item is always Help for it (help.md).
func ContextMenu(c *ui.Ctx, key any, target ui.ID, items []MenuItem) ContextResult {
	res := ContextResult{Chosen: -1}
	id := c.Next(key)
	st := ui.State[contextState](c, id)
	if p := c.Interaction(target).Context; p != nil {
		c.ClaimContext(target)
		st.open, st.at = true, *p
		c.Focus(ui.Child(c.Next(key), "menu")) // the menu, built below
	}
	if !st.open {
		return res
	}
	help := c.Help(target)
	all := items
	if help != "" {
		if len(all) > 0 {
			all = append(all[:len(all):len(all)], MenuItem{Separator: true})
		}
		all = append(all, MenuItem{Label: "? Help", Shortcut: "K", Help: help})
	}
	if catcher(c, struct{ k any }{key}, 900, ContextMenuHelp) {
		st.open = false
		return res
	}
	const w = 250
	at := st.at
	if size := c.Size(); at.X+w > size.X {
		at.X -= w
	}
	var chosen int
	var closed bool
	c.Box(key, ui.Style{Z: 901, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
		chosen, closed = Menu(c, "menu", all, MenuOpts{Width: w, Help: ContextMenuHelp})
	})
	switch {
	case closed:
		st.open = false
	case chosen >= 0 && help != "" && chosen == len(all)-1:
		res.Help, st.open = help, false
		c.RequestHelp(help)
	case chosen >= 0:
		res.Chosen, st.open = chosen, false
	}
	res.Open = st.open
	return res
}

type PopoverOpts struct {
	Label string // its accessible name
	Width float32
	Help  string
}

// Popover shows body in a card under anchor while *open. Escape or a click outside closes it.
func Popover(c *ui.Ctx, key any, anchor ui.ID, open *bool, o PopoverOpts, body func(c *ui.Ctx)) {
	id := c.Next(key)
	st := ui.State[modalState](c, id)
	if !*open {
		st.shown = false
		return
	}
	if hasKey(c.Interaction(id).Keys, "Esc") || catcher(c, struct{ k any }{key}, 800, o.Help) {
		*open, st.shown = false, false
		return
	}
	if !st.shown {
		st.shown = true
		c.Focus(id)
	}
	w := ui.Size{}
	if o.Width > 0 {
		w = ui.Px(o.Width)
	}
	c.Column(key, ui.Style{W: w, Pad: ui.Pad(12), Gap: 8, Fill: col(c, tMenuBg), Stroke: col(c, tMenuBorder), Border: 1, Radius: 8, Z: 801,
		Enter: dropIn, Exit: fade,
		Float: &ui.Float{To: anchor, At: render.Point{Y: 1}, Offset: render.Point{Y: 4}}, Focusable: true, KeyMode: "popover", Help: o.Help,
		Role: ui.RoleDialog, Label: o.Label}, body)
}
