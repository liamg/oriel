package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tChoiceLine    = theme.Declare("choice.line", "line")
	tChoiceOnBg    = theme.DeclareOn("choice.selected.bg", "alpha(accent, 7%)", "bg.surface")
	tChoiceOnLine  = theme.Declare("choice.selected.underline", "accent")
	tChoiceTitle   = theme.Declare("choice.title", "fg")
	tChoiceOnTitle = theme.Declare("choice.selected.title", "accent")
	tChoiceDesc    = theme.Declare("choice.desc", "fg.faint")
)

type choiceSep struct{ i int }

// Choice is one card of a Choices strip.
type Choice struct {
	Title string
	Desc  string // a few words under the title, wrapped
}

type ChoicesOpts struct {
	Help  string
	Label string // the strip's accessible name
}

// Choices is a strip of cards, each a title with a line or two under it, *active lit and underlined in the accent
// (04-launch's modes). A click picks one; with the strip focused, Left/Right (or h/l) move. It reports a change.
func Choices(c *ui.Ctx, key any, items []Choice, active *int, o ChoicesOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	old := *active
	passOn(c, id, in.Keys, func(k render.KeyEvent) bool { return hasKey([]render.KeyEvent{k}, "h", "l", "Left", "Right") })
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "l", "Right"):
			*active = min(*active+1, len(items)-1)
		case hasKey([]render.KeyEvent{k}, "h", "Left"):
			*active = max(*active-1, 0)
		}
	}
	descStyle := monoText(9.5, typeset.Normal, col(c, tChoiceDesc), 0)
	c.Row(key, ui.Style{W: ui.Flex(1), Stroke: col(c, tChoiceLine), Border: 1, Radius: 6, Clip: true, Focusable: true, KeyMode: "tabs",
		Help: o.Help, Role: ui.RoleTabList, Label: o.Label}, func(c *ui.Ctx) {
		for i, it := range items {
			if i > 0 {
				c.Box(choiceSep{i}, ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: col(c, tChoiceLine)}, nil)
			}
			on := i == *active
			fill, title := render.Color{}, col(c, tChoiceTitle)
			if on {
				fill, title = col(c, tChoiceOnBg), col(c, tChoiceOnTitle)
			}
			width := float32(0)
			if r, ok := c.Rect(ui.Child(c.ID(), i)); ok {
				width = r.Dx() - 24
			}
			if Clickable(c, i, ui.Style{Dir: ui.Column, W: ui.Flex(1), H: ui.Flex(1), Fill: fill, Help: o.Help, Role: ui.RoleTab, Label: it.Title, Selected: on},
				func(c *ui.Ctx) {
					c.Column("text", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 10}, Gap: 4, Clip: true}, func(c *ui.Ctx) {
						c.Text("title", it.Title, uiText(c, 13, typeset.Medium, title))
						if it.Desc != "" && width > 0 {
							Wrap(c, "desc", it.Desc, width, descStyle)
						} else if it.Desc != "" {
							c.Text("desc", it.Desc, descStyle)
						}
					})
					if on {
						c.Box("underline", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: col(c, tChoiceOnLine)}, nil)
					}
				}).Clicked {
				*active = i
				c.Focus(id)
			}
		}
	})
	return *active != old
}
