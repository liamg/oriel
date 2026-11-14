package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// TDataCard and TDataCardLine are a data card's ground and border, for what draws a card of its own beside one.
var (
	TDataCard     = theme.Declare("data.card.bg", "bg.surface")
	TDataCardLine = theme.Declare("data.card.line", "line")
)

func dataMono(size float32, col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: col}
}

// DataCard is a card of figures: a kicker, a dim note after it, what's at its right, and its body (a table of
// DataHead and DataCells, or KV rows).
func DataCard(c *ui.Ctx, key any, w ui.Size, title, note string, right func(c *ui.Ctx), body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Column(key, ui.Style{W: w, Pad: ui.Insets{L: 12, R: 12, T: 9, B: 10}, Gap: 8, Fill: th.C(TDataCard), Stroke: th.C(TDataCardLine), Border: 1,
		Radius: 8, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
		c.Row("hd", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14},
				Color: th.C(theme.FgMuted)})
			if note != "" {
				c.Text("n", note, dataMono(10, th.C(theme.FgGhost)))
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if right != nil {
				right(c)
			}
		})
		body(c)
	})
}

// DataCol is a table column: its title and width, right-aligned unless it's the first.
type DataCol struct {
	Title string
	W     float32 // 0 takes what's left
}

// DataColSize is a column's width: w, or what's left for 0.
func DataColSize(w float32) ui.Size {
	if w == 0 {
		return ui.Flex(1)
	}
	return ui.Px(w)
}

// DataHead is a table's header row.
func DataHead(c *ui.Ctx, cols []DataCol) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("cols", ui.Style{W: ui.Flex(1), H: ui.Px(18), Cross: ui.Center}, func(c *ui.Ctx) {
			for i, col := range cols {
				main := ui.End
				if i == 0 {
					main = ui.Start
				}
				c.Row(i, ui.Style{W: DataColSize(col.W), Main: main, Pad: ui.PadXY(6, 0), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", col.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.SemiBold, Tracking: .1},
						Color: th.C(theme.FgGhost)})
				})
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	})
}

// DataCell is a table cell of width w: right-aligned unless it's the first, holding what body draws.
func DataCell(c *ui.Ctx, key any, w float32, first bool, body func(c *ui.Ctx)) {
	main := ui.End
	if first {
		main = ui.Start
	}
	c.Row(key, ui.Style{W: DataColSize(w), Main: main, Cross: ui.Center, Gap: 7, Pad: ui.PadXY(6, 0), Clip: true}, body)
}

// DataText is a right-aligned cell of text in tone.
func DataText(c *ui.Ctx, key any, w float32, s string, tone render.Color) {
	DataCell(c, key, w, false, func(c *ui.Ctx) { c.Text("t", s, dataMono(11, tone)) })
}

// Swatch is a series' small square.
func Swatch(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: tone}, nil)
}

// KV is a row of a small card: a swatch (none for a clear tone) and a name, then values at the right, the last one
// brightest.
func KV(c *ui.Ctx, key any, tone render.Color, name, note string, values ...string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(20), Gap: 8, Cross: ui.Center, Role: ui.RoleText, Label: name}, func(c *ui.Ctx) {
		if tone.A > 0 {
			Swatch(c, "sw", tone)
		}
		c.Text("n", name, dataMono(11, th.C(theme.Fg)))
		if note != "" {
			c.Text("note", note, dataMono(10, th.C(theme.FgGhost)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		for i, v := range values {
			w := float32(44)
			if i == len(values)-1 {
				w = 58
			}
			c.Row(i, ui.Style{W: ui.Px(w), Main: ui.End}, func(c *ui.Ctx) {
				tone := th.C(theme.FgMuted)
				if i == len(values)-1 {
					tone = th.C(theme.Fg)
				}
				c.Text("v", v, dataMono(11, tone))
			})
		}
	})
}
