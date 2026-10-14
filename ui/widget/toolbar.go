package widget

import "github.com/liamg/oriel/ui"

// ToolItem is a toggle on a Toolbar.
type ToolItem struct {
	Key   string
	Label string
	On    *bool
	Help  string
}

type ToolbarOpts struct {
	Help  string
	Label string // its accessible name
}

type toolbarState struct {
	widths map[string]float32 // each chip's width when it last showed
	open   bool
}

const toolGap = 6

// Toolbar is a row of toggle chips that fits the width it's given: what doesn't fit goes behind a "…" button, as a
// menu of the same toggles. It reports whether one changed.
func Toolbar(c *ui.Ctx, key any, items []ToolItem, o ToolbarOpts) bool {
	id := c.Next(key)
	st := ui.State[toolbarState](c, id)
	if st.widths == nil {
		st.widths = map[string]float32{}
	}
	fit := len(items)
	if r, ok := c.Rect(id); ok {
		const more = 22 + toolGap
		var used float32
		for i, it := range items {
			w, known := st.widths[it.Key]
			if !known {
				break
			}
			used += w
			if i > 0 {
				used += toolGap
			}
			room := r.Dx()
			if i < len(items)-1 {
				room -= more
			}
			if used > room {
				fit = i
				break
			}
		}
	}
	changed := false
	var moreID ui.ID
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: toolGap, Cross: ui.Center, Clip: true, Role: ui.RoleToolbar, Label: o.Label}, func(c *ui.Ctx) {
		for _, it := range items[:fit] {
			cid := c.Next(it.Key)
			if v := ToggleChip(c, it.Key, it.Label, *it.On, ToggleChipOpts{Help: it.Help}); v != *it.On {
				*it.On, changed = v, true
			}
			if r, ok := c.Rect(cid); ok {
				st.widths[it.Key] = r.Dx()
			}
		}
		if fit < len(items) {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			moreID = c.Next("more")
			if IconButton(c, "more", "…", IconButtonOpts{Label: "More", Help: o.Help, Size: 22}) {
				st.open = !st.open
			}
		} else {
			st.open = false
		}
	})
	if fit < len(items) && st.open {
		rest := items[fit:]
		Popover(c, struct{ k any }{key}, moreID, &st.open, PopoverOpts{Label: "More", Help: o.Help}, func(c *ui.Ctx) {
			menu := make([]MenuItem, len(rest))
			for i, it := range rest {
				mark := "  "
				if *it.On {
					mark = "✓ "
				}
				menu[i] = MenuItem{Label: mark + it.Label, Help: it.Help}
			}
			if chosen, closed := Menu(c, "menu", menu, MenuOpts{Width: 200, Help: o.Help}); chosen >= 0 {
				*rest[chosen].On, changed = !*rest[chosen].On, true
			} else if closed {
				st.open = false
			}
		})
	}
	return changed
}
