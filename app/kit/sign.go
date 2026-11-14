package kit

import (
	"cmp"
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// DrawSign draws a sign: a box, its text, or an icon with its text small beside it. One with a command runs it when
// clicked, one with a Menu offers it on a right-click, and one with a Hover shows that card while the pointer is on it.
func DrawSign(c *ui.Ctx, h plugin.Host, key any, d uireg.Decoration, size float32) {
	th := c.Theme()
	col := TokenColour(th, d.Style, th.C(theme.FgMuted))
	draw := func(c *ui.Ctx) {
		switch {
		case d.Icon != "":
			r, _ := ui.Service[registry.Registry](c)
			Icon(c, r, "icon", d.Icon, 12, col)
			if d.Text != "" {
				c.Box("n", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: 1}, Self: render.Point{X: .5, Y: .7}}}, func(c *ui.Ctx) {
					c.Text("t", d.Text, MonoFont(8, col))
				})
			}
		case d.Text == "":
			c.Box("box", ui.Style{W: ui.Px(12), H: ui.Px(12), Radius: 3, Fill: col}, nil)
		default:
			c.Text("t", d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: col})
		}
	}
	if d.Command == "" && d.Hover == nil && len(d.Menu) == 0 {
		c.Box(key, ui.Style{}, draw)
		return
	}
	tip := cmp.Or(d.Tooltip, d.Command)
	st := ui.Style{Radius: 3, Label: tip, Help: "editor/pane"}
	if d.Hover == nil {
		st.Tooltip = tip
	}
	id := c.Next(key)
	in := widget.Clickable(c, key, st, draw)
	if in.Clicked && d.Command != "" {
		runSign(h, signCommand(d, in.Mods), d.Args)
	}
	if len(d.Menu) > 0 {
		signMenu(c, h, key, id, in.Context != nil, d.Menu)
	}
	if d.Hover != nil {
		widget.HoverCard(c, struct{ k any }{key}, id, widget.HoverCardOpts{Help: "editor/pane#threads", Width: 380}, func(c *ui.Ctx) {
			if f, ok := d.Hover.(*ui.Func); ok {
				f.Fn(c)
				return
			}
			c.Node(d.Hover)
		})
	}
}

// signCommand is what a click on a sign runs with mods held: its Modified command for them, else its Command.
func signCommand(d uireg.Decoration, mods render.Mods) string {
	for _, m := range []struct {
		mod  render.Mods
		name string
	}{{render.ModShift, "shift"}, {render.ModAlt, "alt"}, {render.ModCtrl, "ctrl"}, {render.ModSuper, "super"}} {
		if mods&m.mod != 0 && d.Modified[m.name] != "" {
			return d.Modified[m.name]
		}
	}
	return d.Command
}

// signMenuState is whether a sign's menu is open, and the items it opened with.
type signMenuState struct {
	open  bool
	items []uireg.SignItem
}

// signMenu is a sign's right-click menu: its items whose commands are available, asked when it opens.
func signMenu(c *ui.Ctx, h plugin.Host, key any, id ui.ID, clicked bool, menu []uireg.SignItem) {
	st := ui.State[signMenuState](c, id)
	if clicked {
		st.items = st.items[:0]
		for _, it := range menu {
			if h.Available(it.Command, it.Args) {
				st.items = append(st.items, it)
			}
		}
	}
	if !clicked && !st.open {
		return
	}
	entries := make([]widget.MenuItem, len(st.items))
	for i, it := range st.items {
		entries[i] = widget.MenuItem{Label: it.Label, Help: widget.ContextMenuHelp}
	}
	res := widget.ContextMenu(c, struct{ menu any }{key}, id, entries)
	st.open = res.Open
	if res.Chosen >= 0 && res.Chosen < len(st.items) {
		runSign(h, st.items[res.Chosen].Command, st.items[res.Chosen].Args)
	}
}

func runSign(h plugin.Host, command string, args registry.Args) {
	if err := h.Run(context.Background(), command, args); err != nil {
		h.Notify(core.Notice{ID: "editor.sign", Severity: core.SevWarning, AutoClear: true, Text: err.Error()})
	}
}
