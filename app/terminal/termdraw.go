package terminal

import (
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/theme"
)

// A terminal's colours.
var (
	TTermBg     = theme.Declare("terminal.bg", "bg.base")
	TTermFg     = theme.Declare("terminal.fg", "fg")
	TTermCursor = theme.Declare("terminal.cursor", "accent")
	// The 16 ANSI colours, which a theme's terminal palette sets.
	TTermANSI = func() [16]theme.TokenID {
		defs := [16]string{"bg2", "red", "green", "amber", "blue", "violet", "cyan", "fg2",
			"fg4", "lighten(red, 8%)", "lighten(green, 8%)", "accent", "lighten(blue, 8%)", "pink", "lighten(cyan, 8%)", "fg"}
		var out [16]theme.TokenID
		for i, d := range defs {
			out[i] = theme.Declare("terminal.ansi."+strconv.Itoa(i), d)
		}
		return out
	}()
)

// termColour is a cell colour in the theme's terminal palette.
func termColour(th *theme.Resolved, c term.Color, def color.NRGBA) color.NRGBA {
	switch c.Kind() {
	case term.ColorPalette:
		i := c.Index()
		if i < 16 {
			return th.C(TTermANSI[i])
		}
		return xterm256(i)
	case term.ColorRGB:
		r, g, b := c.RGB()
		return color.NRGBA{R: r, G: g, B: b, A: 255}
	}
	return def
}

// xterm256 is a colour of xterm's 6×6×6 cube or its greys.
func xterm256(i uint8) color.NRGBA {
	if i >= 232 {
		v := uint8(8 + 10*(int(i)-232))
		return color.NRGBA{R: v, G: v, B: v, A: 255}
	}
	i -= 16
	step := func(n uint8) uint8 {
		if n == 0 {
			return 0
		}
		return 55 + 40*n
	}
	return color.NRGBA{R: step(i / 36), G: step(i / 6 % 6), B: step(i % 6), A: 255}
}

type termRun struct {
	from      int
	text      strings.Builder
	fg, bg    color.NRGBA
	attrs     term.Attrs
	hasBg     bool
	cells     int
	underline term.Underline
}

// drawCells draws a row of cells in base, lineH high: backgrounds, then text in runs of one style, each at its
// cell's column so wide characters keep the grid, and box-drawing and block characters drawn as shapes so they join.
func drawCells(f render.Frame, sh typeset.Shaper, th *theme.Resolved, cells []term.Cell, x0, y, charW float32, base typeset.FontSpec, lineH float32) {
	defFg := th.C(TTermFg)
	var run *termRun
	flush := func() {
		if run == nil || run.text.Len() == 0 {
			run = nil
			return
		}
		font := base
		if run.attrs&term.Bold != 0 {
			font.Weight = typeset.Bold
		}
		if run.attrs&term.Italic != 0 {
			font.Italic = true
		}
		col := run.fg
		if run.attrs&term.Faint != 0 {
			col = theme.WithAlpha(col, .6)
		}
		g := sh.Shape(strings.TrimRight(run.text.String(), " "), font)
		f.Glyphs(g, render.Point{X: x0 + float32(run.from)*charW, Y: y + (lineH+g.Ascent-g.Descent)/2}, col)
		if run.underline != term.UnderlineNone || run.attrs&term.Strikethrough != 0 {
			uy := y + lineH - 3
			if run.attrs&term.Strikethrough != 0 {
				uy = y + lineH/2
			}
			f.Rect(render.Rect{Min: render.Point{X: x0 + float32(run.from)*charW, Y: uy},
				Max: render.Point{X: x0 + float32(run.from+run.cells)*charW, Y: uy + 1}}, render.Style{Fill: col})
		}
		run = nil
	}
	for col, cell := range cells {
		if cell.Width == term.SpacerTail || cell.Width == term.SpacerHead {
			continue
		}
		fg, bg := termColour(th, cell.FG, defFg), termColour(th, cell.BG, color.NRGBA{})
		if cell.Attrs&term.Inverse != 0 {
			fg, bg = bg, fg
			if fg.A == 0 {
				fg = th.C(TTermBg)
			}
		}
		w := 1
		if cell.Width == term.Wide {
			w = 2
		}
		cx := x0 + float32(col)*charW
		if bg.A > 0 {
			f.Rect(render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + float32(w)*charW, Y: y + lineH}}, render.Style{Fill: bg})
		}
		r, _ := utf8.DecodeRuneInString(cell.Text)
		if boxGlyph(f, r, render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + float32(w)*charW, Y: y + lineH}}, fg) {
			flush()
			continue
		}
		if cell.Text == "" || cell.Text == " " {
			if run != nil && run.fg == fg && run.attrs == cell.Attrs && run.from+run.cells == col {
				run.text.WriteByte(' ') // a space inside a run; trailing ones are trimmed
				run.cells++
			}
			continue
		}
		if run == nil || run.fg != fg || run.attrs != cell.Attrs || run.underline != cell.Underline || w != 1 || run.from+run.cells != col {
			flush()
			run = &termRun{from: col, fg: fg, attrs: cell.Attrs, underline: cell.Underline}
		}
		run.text.WriteString(cell.Text)
		run.cells += w
		if w != 1 {
			flush()
		}
	}
	flush()
}

// boxGlyph draws a box-drawing or block character as shapes filling its cell, as Ghostty and kitty do, so lines meet
// at any size; it reports whether r was one it draws.
func boxGlyph(f render.Frame, r rune, cell render.Rect, c color.NRGBA) bool {
	cx, cy := (cell.Min.X+cell.Max.X)/2, (cell.Min.Y+cell.Max.Y)/2
	hline := func(x1, x2, wgt float32) {
		f.Rect(render.Rect{Min: render.Point{X: x1, Y: cy - wgt/2}, Max: render.Point{X: x2, Y: cy + wgt/2}}, render.Style{Fill: c})
	}
	vline := func(y1, y2, wgt float32) {
		f.Rect(render.Rect{Min: render.Point{X: cx - wgt/2, Y: y1}, Max: render.Point{X: cx + wgt/2, Y: y2}}, render.Style{Fill: c})
	}
	// Each light or heavy line character is which of its four arms it has: left, right, up, down.
	arms := map[rune][4]bool{
		'─': {true, true, false, false}, '━': {true, true, false, false}, '│': {false, false, true, true}, '┃': {false, false, true, true},
		'┌': {false, true, false, true}, '┐': {true, false, false, true}, '└': {false, true, true, false}, '┘': {true, false, true, false},
		'├': {false, true, true, true}, '┤': {true, false, true, true}, '┬': {true, true, false, true}, '┴': {true, true, true, false},
		'┼': {true, true, true, true}, '┏': {false, true, false, true}, '┓': {true, false, false, true}, '┗': {false, true, true, false},
		'┛': {true, false, true, false}, '╭': {false, true, false, true}, '╮': {true, false, false, true}, '╰': {false, true, true, false},
		'╯': {true, false, true, false},
	}
	if a, ok := arms[r]; ok {
		wgt := float32(1)
		if strings.ContainsRune("━┃┏┓┗┛", r) {
			wgt = 2
		}
		if a[0] {
			hline(cell.Min.X, cx+wgt/2, wgt)
		}
		if a[1] {
			hline(cx-wgt/2, cell.Max.X, wgt)
		}
		if a[2] {
			vline(cell.Min.Y, cy+wgt/2, wgt)
		}
		if a[3] {
			vline(cy-wgt/2, cell.Max.Y, wgt)
		}
		return true
	}
	fill := func(x1, y1, x2, y2 float32, a float64) {
		f.Rect(render.Rect{Min: render.Point{X: x1, Y: y1}, Max: render.Point{X: x2, Y: y2}}, render.Style{Fill: theme.WithAlpha(c, a)})
	}
	switch r {
	case '█':
		fill(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Max.Y, 1)
	case '▀':
		fill(cell.Min.X, cell.Min.Y, cell.Max.X, cy, 1)
	case '▄':
		fill(cell.Min.X, cy, cell.Max.X, cell.Max.Y, 1)
	case '▌':
		fill(cell.Min.X, cell.Min.Y, cx, cell.Max.Y, 1)
	case '▐':
		fill(cx, cell.Min.Y, cell.Max.X, cell.Max.Y, 1)
	case '░':
		fill(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Max.Y, .25)
	case '▒':
		fill(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Max.Y, .5)
	case '▓':
		fill(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Max.Y, .75)
	default:
		return false
	}
	return true
}
