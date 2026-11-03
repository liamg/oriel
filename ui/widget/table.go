package widget

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tTableBorder = theme.Declare("table.border", "line.strong")
	tTableRule   = theme.Declare("table.rule", "line")
	tTableHeadBg = theme.DeclareOn("table.head.bg", "alpha(fg, 5%)", "bg.base")
	tTableHeadFg = theme.Declare("table.head.fg", "fg")
)

type TableOpts struct {
	Align []ui.Align            // each column's: Start by default
	Head  bool                  // the first row is the header: semibold on a fill
	Label string                // its accessible name
	Code  ui.TextStyle          // code spans' text, as Rich's
	Row   func(r int, id ui.ID) // told each row's node as it's built, for a caller that maps rows to something
}

const (
	tablePadX = 10
	tablePadY = 6
)

// Table draws rows of cells, each a Rich paragraph in base, in a bordered grid: columns as wide as their content
// when there's room, else narrowed (wrapping their text) to fit width, and scrolling sideways when even their
// longest words don't fit. A short row's missing cells are empty.
func Table(c *ui.Ctx, key any, rows [][][]Span, width float32, base ui.TextStyle, o TableOpts) {
	cols := 0
	for _, r := range rows {
		cols = max(cols, len(r))
	}
	if cols == 0 {
		return
	}
	head := base
	head.Font.Weight, head.Color = typeset.SemiBold, col(c, tTableHeadFg)
	style := func(r int) ui.TextStyle {
		if r == 0 && o.Head {
			return head
		}
		return base
	}
	ropts := RichOpts{Code: o.Code}
	natural, least := make([]float32, cols), make([]float32, cols)
	for r, row := range rows {
		for j, cell := range row {
			line, widest := richMeasure(c, cell, style(r), ropts).widths()
			natural[j], least[j] = max(natural[j], line), max(least[j], widest)
		}
	}
	chrome := float32(cols+1) + float32(cols)*2*tablePadX // the rules between columns, the border, the cells' padding
	inner := tableWidths(natural, least, width-chrome)
	total := chrome
	for _, w := range inner {
		total += w
	}
	table := func(c *ui.Ctx, key any) {
		c.Column(key, ui.Style{W: ui.Px(total), Pad: ui.Pad(1), Stroke: col(c, tTableBorder), Border: 1, Radius: 4, Clip: true, Role: ui.RoleGroup,
			Label: o.Label}, func(c *ui.Ctx) {
			for r := range rows {
				if r > 0 {
					c.Box(fmt.Sprint("rule", r), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tTableRule)}, nil)
				}
				st := ui.Style{W: ui.Flex(1), Role: ui.RoleListItem, Label: tableRowText(rows[r])}
				if r == 0 && o.Head {
					st.Fill = col(c, tTableHeadBg)
				}
				id := c.Row(r, st, func(c *ui.Ctx) {
					for j := range cols {
						if j > 0 {
							c.Box(fmt.Sprint("rule", j), ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: col(c, tTableRule)}, nil)
						}
						var cell []Span
						if j < len(rows[r]) {
							cell = rows[r][j]
						}
						align := ui.Start
						if j < len(o.Align) {
							align = o.Align[j]
						}
						c.Column(j, ui.Style{W: ui.Px(inner[j] + 2*tablePadX), H: ui.Flex(1), Pad: ui.PadXY(tablePadX, tablePadY), Cross: align}, func(c *ui.Ctx) {
							a := ropts
							a.Align = align
							Rich(c, "t", cell, inner[j], style(r), a)
						})
					}
				})
				if o.Row != nil {
					o.Row(r, id)
				}
			}
		})
	}
	if total <= width+.5 {
		table(c, key)
		return
	}
	c.Row(key, ui.Style{W: ui.Px(width), ScrollX: true}, func(c *ui.Ctx) { table(c, "table") })
}

// tableWidths fits columns into room: each its natural width if they all fit; otherwise the widest are capped at
// one width, so the short columns stay whole and the long ones wrap, none narrower than its least.
func tableWidths(natural, least []float32, room float32) []float32 {
	capped := func(w float32) (out []float32, sum float32) {
		out = make([]float32, len(natural))
		for j := range natural {
			out[j] = max(min(natural[j], w), least[j])
			sum += out[j]
		}
		return out, sum
	}
	var lo, hi float32
	for _, w := range natural {
		hi = max(hi, w)
	}
	if _, sum := capped(hi); sum <= room {
		return natural
	}
	for range 30 {
		mid := (lo + hi) / 2
		if _, sum := capped(mid); sum <= room {
			lo = mid
		} else {
			hi = mid
		}
	}
	out, _ := capped(lo)
	return out
}

func tableRowText(row [][]Span) string {
	cells := make([]string, len(row))
	for i, c := range row {
		cells[i] = richText(c)
	}
	return strings.Join(cells, " · ")
}
