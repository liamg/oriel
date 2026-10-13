package app

import (
	"context"
	"fmt"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// RailView is the icon strip down the window's left edge (ui.md 01f): the rail entries, built-in or a plugin's.
const RailView = "app.rail"

const railW = 44

var (
	tRailBg     = theme.Declare("rail.bg", "bg.surface")
	tRailIcon   = theme.Declare("rail.icon", "fg.faint")
	tRailOn     = theme.Declare("rail.icon.active", "accent")
	tRailOnBg   = theme.DeclareOn("rail.icon.active.bg", "alpha(accent, 8%)", "bg.surface")
	tRailOff    = theme.Declare("rail.icon.unavailable", "fg.ghost")
	tRailBadge  = theme.Declare("rail.badge", "amber")
	tRailBadgeF = theme.Declare("rail.badge.fg", "ink")
)

// rail is the rail and its built-in entries: the main views of 01-ide, each lit once something registers it.
type rail struct{ disposer }

func (r *rail) Manifest() ext.Manifest { return ext.Manifest{ID: "rail"} }

// railViews are the built-in entries that toggle a view in the left dock, with their keys.
var railViews = []struct{ name, icon, title, view, keys string }{
	{"explorer", "files", "Explorer", "explorer", "<D-E>"},
	{"git", "git", "Git", "git", "<D-G>"},
	{"requests", "requests", "Requests", "requests", "<D-R>"},
	{"debug", "debug", "Debug", "debug", "<D-D>"},
	{"tests", "tests", "Tests", "tests", "<D-T>"},
}

func (r *rail) Activate(_ context.Context, h ext.Host) error {
	shown := func(id string) func() bool {
		return func() bool {
			for _, v := range h.Layout().Visible() {
				if v.ID == id {
					return true
				}
			}
			return false
		}
	}
	order := map[string]int{"explorer": 10, "search": 20, "git": 30, "requests": 40, "debug": 50, "tests": 60}
	for _, v := range railViews {
		args := registry.Args{"id": v.view, "place": "dock", "edge": "left"}
		r.add(h.Contribute(registry.RailEntry{Name: v.name, Icon: v.icon, Title: v.title, Command: "view.toggle", Args: args,
			Order: order[v.name], Active: shown(v.view)}))
		r.add(h.Keymap(registry.Keymap{Mode: "global", Keys: v.keys, Command: "view.toggle", Args: args, Desc: v.title}))
	}
	search := registry.Args{"source": "grep"}
	r.add(h.Contribute(registry.RailEntry{Name: "search", Icon: "search", Title: "Search", Command: "picker.open", Args: search, Order: order["search"]}))
	r.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-F>", Command: "picker.open", Args: search, Desc: "Search"}))
	r.add(h.Contribute(registry.RailEntry{Name: "settings", Icon: "settings", Title: "Settings", Command: "settings.open", Order: 10, Bottom: true}))
	r.add(h.View(RailView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "rail", Fn: func(c *ui.Ctx) { r.build(c, h) }}
	})))
	return nil
}

func (r *rail) build(c *ui.Ctx, h ext.Host) {
	th := c.Theme()
	es := registry.RailEntries(h)
	c.Row("rail", ui.Style{W: ui.Px(railW), H: ui.Flex(1), Fill: th.C(tRailBg), Role: ui.RoleToolbar, Label: "Views"}, func(c *ui.Ctx) {
		c.Column("icons", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{T: 8, B: 8}, Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
			for i, e := range es {
				if e.Bottom && (i == 0 || !es[i-1].Bottom) {
					c.Box("gap", ui.Style{H: ui.Flex(1)}, nil)
				}
				r.entry(c, h, e)
			}
		})
		c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
	})
}

func (r *rail) entry(c *ui.Ctx, h ext.Host, e registry.RailEntry) {
	th := c.Theme()
	ok := h.Available(e.Command, e.Args)
	on := ok && e.Active != nil && e.Active()
	id := c.Next(e.Name)
	if ok && c.Interaction(id).Clicked {
		if err := h.Run(context.Background(), e.Command, e.Args); err != nil {
			h.Log().Error("rail", "entry", e.Name, "err", err)
		}
	}
	col, fill := th.C(tRailIcon), render.Color{}
	switch {
	case on:
		col, fill = th.C(tRailOn), th.C(tRailOnBg)
	case !ok:
		col = th.C(tRailOff)
	}
	tip := e.Title
	if k := boundKey(h, e.Command, e.Args); k != "" {
		tip += "  " + k
	}
	if !ok {
		tip += " (not available yet)"
	}
	c.Box(e.Name, ui.Style{W: ui.Px(32), H: ui.Px(32), Radius: 7, Fill: fill, Main: ui.Center, Cross: ui.Center, Interactive: ok, Disabled: !ok,
		Cursor: render.CursorPointer, Tooltip: tip, Role: ui.RoleButton, Label: e.Title, Selected: on, Help: "shell/rail"}, func(c *ui.Ctx) {
		icon(c, "icon", e.Icon, 16, col)
		if n := badge(e); n > 0 {
			c.Box("badge", ui.Style{H: ui.Px(13), Pad: ui.PadXY(3, 0), Radius: 6.5, Fill: th.C(tRailBadge), Cross: ui.Center,
				Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 1}, Offset: render.Point{X: -2, Y: 3}}}, func(c *ui.Ctx) {
				c.Text("n", fmt.Sprint(n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 8.5, Weight: typeset.Bold}, Color: th.C(tRailBadgeF)})
			})
		}
	})
}

func badge(e registry.RailEntry) int {
	if e.Badge == nil {
		return 0
	}
	n := 0
	registry.Call(func() error { n = e.Badge(); return nil })
	return n
}

// boundKey is the key bound to command with args right now, as the keyboard shows it ("⌘⇧E"), or "".
func boundKey(h ext.Host, command string, args registry.Args) string {
	for _, k := range h.Keymaps() {
		if k.Command == command && (k.Mode == "global" || k.Mode == "n") && sameArgs(k.Args, args) {
			return keyLabel(k.Keys)
		}
	}
	return ""
}

func sameArgs(a, b registry.Args) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if fmt.Sprint(b[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}
