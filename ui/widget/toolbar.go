package widget

import "github.com/liamg/oriel/ui"

// ToolItem is a toggle on a Toolbar.
type ToolItem struct {
	Key      string
	Label    string
	On       *bool
	Help     string
	Shortcut string // shown when it's in a menu
}

type ToolbarOpts struct {
	Help  string
	Label string // its accessible name
}

type toolbarState struct {
	widths map[string]float32 // each chip's width when it last showed
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
			if ToggleMenu(c, "more", items[fit:], MenuButtonOpts{Label: "More", Glyph: "…", Size: 22, Width: 200, Help: o.Help}) {
				changed = true
			}
		}
	})
	return changed
}
