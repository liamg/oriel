package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

type RowsOpts struct {
	Help     string
	Label    string
	KeyMode  string             // the keymap mode it takes keys in; "list" by default
	Height   ui.Size            // fits its rows by default
	Static   func(i int) bool   // rows that can't be selected or opened: a divider, a heading
	Grow     func(i int) bool   // static rows that take the height left, to push the rest down
	Disabled func(i int) bool   // rows shown dimmed that don't open
	RowLabel func(i int) string // a row's accessible name
	Tooltip  func(i int) string // a row's tooltip, or ""
}

// Rows is a short list of rows of their own heights, all built, with a selection in *sel: j/k or Down/Up move it
// over the rows that aren't static, Enter opens it, and a click selects and opens a row. The selection shows while
// the rows have focus; a row draws whatever else marks it (the one that's open).
func Rows(c *ui.Ctx, key any, n int, sel *int, o RowsOpts, row func(c *ui.Ctx, i int, selected bool)) ListResult {
	res := ListResult{Activated: -1}
	static := func(i int) bool { return o.Static != nil && o.Static(i) }
	off := func(i int) bool { return o.Disabled != nil && o.Disabled(i) }
	str := func(f func(int) string, i int) string {
		if f == nil {
			return ""
		}
		return f(i)
	}
	step := func(from, by int) int {
		for i := from + by; i >= 0 && i < n; i += by {
			if !static(i) {
				return i
			}
		}
		return from
	}
	id := c.Next(key)
	in := c.Interaction(id)
	old := *sel
	if *sel >= n || *sel < 0 || static(*sel) {
		*sel = step(-1, 1)
	}
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "j", "Down"):
			*sel = step(*sel, 1)
		case hasKey([]render.KeyEvent{k}, "k", "Up"):
			*sel = step(*sel, -1)
		case hasKey([]render.KeyEvent{k}, "g", "Home"):
			*sel = step(-1, 1)
		case hasKey([]render.KeyEvent{k}, "G", "End"):
			*sel = step(n, -1)
		case k.Key == "CR":
			if n > 0 && !static(*sel) && !off(*sel) {
				res.Activated = *sel
			}
		}
	}
	focused := in.Focused
	mode := o.KeyMode
	if mode == "" {
		mode = "list"
	}
	c.Column(key, ui.Style{W: ui.Flex(1), H: o.Height, Focusable: true, Help: o.Help, KeyMode: mode, Role: ui.RoleList, Label: o.Label}, func(c *ui.Ctx) {
		for i := range n {
			if static(i) {
				st := ui.Style{W: ui.Flex(1), Dir: ui.Column}
				if o.Grow != nil && o.Grow(i) {
					st.H = ui.Flex(1)
				}
				c.Box(i, st, func(c *ui.Ctx) { row(c, i, false) })
				continue
			}
			item := c.Next(i)
			ri := c.Interaction(item)
			if ri.Clicked {
				*sel, res.Activated = i, i
				c.Focus(id)
			}
			on := i == *sel && focused
			fill := render.Color{}
			switch {
			case on:
				fill = col(c, tRowSelected)
			case ri.Hovered:
				fill = col(c, tRowHover)
			}
			c.Row(i, ui.Style{W: ui.Flex(1), Fill: fill, Cross: ui.Center, Interactive: true, NoHover: true, Cursor: render.CursorPointer,
				Role: ui.RoleListItem, Selected: i == *sel, Tooltip: str(o.Tooltip, i), Label: str(o.RowLabel, i), Disabled: off(i),
				Help: o.Help}, func(c *ui.Ctx) {
				row(c, i, on)
			})
		}
	})
	res.Changed = *sel != old
	return res
}
