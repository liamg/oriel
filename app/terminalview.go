package app

import (
	"image/color"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tTermBg     = theme.Declare("terminal.bg", "bg.base")
	tTermFg     = theme.Declare("terminal.fg", "fg")
	tTermCursor = theme.Declare("terminal.cursor", "accent")
	tTermDotOK  = theme.Declare("terminal.exit.ok", "ok")
	tTermDotBad = theme.Declare("terminal.exit.failed", "error")
	tTermMode   = theme.Declare("statusline.mode.terminal", "status.quiet")
	// The 16 ANSI colours, which a theme's terminal palette sets (theming.md).
	tTermANSI = func() [16]theme.TokenID {
		defs := [16]string{"bg2", "red", "green", "amber", "blue", "violet", "cyan", "fg2",
			"fg4", "lighten(red, 8%)", "lighten(green, 8%)", "accent", "lighten(blue, 8%)", "pink", "lighten(cyan, 8%)", "fg"}
		var out [16]theme.TokenID
		for i, d := range defs {
			out[i] = theme.Declare("terminal.ansi."+strconv.Itoa(i), d)
		}
		return out
	}()
)

const (
	termFontSize = 12.5
	termLineH    = 17
	termGutter   = 14 // the exit dots
)

var termFont = typeset.FontSpec{Family: typeset.Mono, Size: termFontSize}

type terminalView struct{ x *terminals }

func (v terminalView) EdgeToEdge() {}

func (v terminalView) Title(instance string) string {
	if t := v.x.get(instance); t != nil {
		return t.title()
	}
	return "terminal"
}

// ModeLabel is the status line's mode chip while a terminal has focus.
func (v terminalView) ModeLabel(instance string) (string, string) {
	if t := v.x.get(instance); t != nil && t.normal {
		return "TERM-NORMAL", tTermMode.Name()
	}
	return "TERMINAL", tTermMode.Name()
}

func (v terminalView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "terminal", Fn: func(c *ui.Ctx) {
		t := v.x.get(vc.Instance)
		if t == nil {
			c.Text("gone", "this terminal has closed", ui.TextStyle{})
			return
		}
		v.x.draw(c, t, vc.Focused)
	}}
}

// colour is a cell colour in the theme's terminal palette.
func termColour(th *theme.Resolved, c term.Color, def color.NRGBA) color.NRGBA {
	switch c.Kind() {
	case term.ColorPalette:
		i := c.Index()
		if i < 16 {
			return th.C(tTermANSI[i])
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

// draw is the terminal: keys to the program (or to vim in terminal-normal mode), resized to its pane, its grid drawn
// in one paint, and the exit dots of its commands down the left.
func (x *terminals) draw(c *ui.Ctx, t *terminal, focused bool) {
	th := c.Theme()
	charW := c.Shaper().Shape("M", termFont).Advance
	id := c.Next("term")
	in := c.Interaction(id)
	for _, k := range in.Keys {
		x.key(c, t, k)
	}
	if in.Scroll.Y != 0 && !t.normal {
		t.emu.Scroll(term.Scroll{To: term.ScrollDelta, Delta: int(in.Scroll.Y / termLineH)})
	}
	if r, ok := c.Rect(id); ok {
		cols, rows := max(int((r.Dx()-termGutter-8)/charW), 2), max(int((r.Dy()-6)/termLineH), 2)
		if cols != t.cols || rows != t.rows {
			t.cols, t.rows = cols, rows
			t.emu.Resize(cols, rows)
			t.pty.Resize(cols, rows)
		}
	}
	if focused && !in.Focused {
		c.Focus(id)
	}
	mode := TerminalKeyMode
	if t.normal {
		mode = TerminalNormalKeyMode
	}
	c.Box("term", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tTermBg), Clip: true, Focusable: true, KeyMode: mode, Wheel: true,
		Cursor: render.CursorText, Help: "terminal", Role: ui.RoleTextBox, Label: t.title()}, func(c *ui.Ctx) {
		if t.normal {
			x.drawNormal(c, t, charW)
			return
		}
		grid := t.emu.Grid()
		cs := t.emu.CursorState()
		sb := t.emu.Scrollbar()
		dots := t.exitDots()
		c.Paint("grid", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
			x0 := r.Min.X + termGutter
			for row, line := range grid.Lines {
				y := r.Min.Y + 3 + float32(row)*termLineH
				if d, ok := dots[sb.Offset+row]; ok {
					tok := tTermDotOK
					if d == "failed" {
						tok = tTermDotBad
					}
					if d != "running" {
						f.Rect(render.Rect{Min: render.Point{X: r.Min.X + 4, Y: y + 6}, Max: render.Point{X: r.Min.X + 9, Y: y + 11}},
							render.Style{Fill: th.C(tok), Radius: 2.5})
					}
				}
				drawTermLine(f, c.Shaper(), th, line.Cells, x0, y, charW)
			}
			if cs.InViewport && cs.Visible {
				cx, cy := x0+float32(cs.ViewX)*charW, r.Min.Y+3+float32(cs.ViewY)*termLineH
				rc := render.Rect{Min: render.Point{X: cx, Y: cy}, Max: render.Point{X: cx + charW, Y: cy + termLineH}}
				switch cs.Shape {
				case term.ShapeBar:
					rc.Max.X = cx + 2
				case term.ShapeUnderline:
					rc.Min.Y = cy + termLineH - 2
				}
				f.Rect(rc, render.Style{Fill: theme.WithAlpha(th.C(tTermCursor), .85)})
			}
		})
		if t.done {
			c.Box("exited", ui.Style{Pad: ui.PadXY(8, 2), Radius: 4, Fill: th.C(theme.BgRaised), Float: &ui.Float{At: render.Point{X: 1, Y: 1},
				Self: render.Point{X: 1, Y: 1}, Offset: render.Point{X: -8, Y: -6}}}, func(c *ui.Ctx) {
				c.Text("t", "process exited", ui.TextStyle{Font: termFont, Color: th.C(theme.FgFaint)})
			})
		}
	})
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

// drawTermLine draws a row of cells: backgrounds, then text in runs of one style, each at its cell's column so wide
// characters keep the grid, and box-drawing and block characters drawn as shapes so they join.
func drawTermLine(f render.Frame, sh typeset.Shaper, th *theme.Resolved, cells []term.Cell, x0, y, charW float32) {
	defFg := th.C(tTermFg)
	var run *termRun
	flush := func() {
		if run == nil || run.text.Len() == 0 {
			run = nil
			return
		}
		font := termFont
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
		f.Glyphs(g, render.Point{X: x0 + float32(run.from)*charW, Y: y + (termLineH+g.Ascent-g.Descent)/2}, col)
		if run.underline != term.UnderlineNone || run.attrs&term.Strikethrough != 0 {
			uy := y + termLineH - 3
			if run.attrs&term.Strikethrough != 0 {
				uy = y + termLineH/2
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
				fg = th.C(tTermBg)
			}
		}
		w := 1
		if cell.Width == term.Wide {
			w = 2
		}
		cx := x0 + float32(col)*charW
		if bg.A > 0 {
			f.Rect(render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + float32(w)*charW, Y: y + termLineH}}, render.Style{Fill: bg})
		}
		r, _ := utf8.DecodeRuneInString(cell.Text)
		if boxGlyph(f, r, render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + float32(w)*charW, Y: y + termLineH}}, fg) {
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

// drawNormal is terminal-normal mode: the scrollback as vim's window shows it, with its cursor and Visual area.
func (x *terminals) drawNormal(c *ui.Ctx, t *terminal, charW float32) {
	th := c.Theme()
	p, ok := t.vim.Pane(t.vim.WindowID())
	if !ok {
		return
	}
	t.vim.SetRows(p.ID(), t.rows)
	snap := p.Buf()
	top := p.Top()
	cur := p.Cursor()
	dots := t.exitDots()
	c.Paint("lines", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
		x0 := r.Min.X + termGutter
		for row := 0; row < t.rows && top+row < snap.Lines(); row++ {
			line := top + row
			y := r.Min.Y + 3 + float32(row)*termLineH
			if from, to, eol, sel := p.Selected(line); sel {
				a, z := p.VCol(line, from), p.VCol(line, to)
				if eol {
					z++
				}
				f.Rect(render.Rect{Min: render.Point{X: x0 + float32(a)*charW, Y: y}, Max: render.Point{X: x0 + float32(z)*charW, Y: y + termLineH}},
					render.Style{Fill: th.C(theme.Selection)})
			}
			if d, ok := dots[line]; ok && d != "running" {
				tok := tTermDotOK
				if d == "failed" {
					tok = tTermDotBad
				}
				f.Rect(render.Rect{Min: render.Point{X: r.Min.X + 4, Y: y + 6}, Max: render.Point{X: r.Min.X + 9, Y: y + 11}},
					render.Style{Fill: th.C(tok), Radius: 2.5})
			}
			if s := string(snap.Line(line)); s != "" {
				g := c.Shaper().Shape(s, termFont)
				f.Glyphs(g, render.Point{X: x0, Y: y + (termLineH+g.Ascent-g.Descent)/2}, th.C(tTermFg))
			}
			if line == cur.Line {
				cx := x0 + float32(p.VCol(line, cur.Col))*charW
				f.Rect(render.Rect{Min: render.Point{X: cx, Y: y}, Max: render.Point{X: cx + charW, Y: y + termLineH}},
					render.Style{Fill: theme.WithAlpha(th.C(tTermCursor), .7)})
			}
		}
	})
}

// key handles a key at the terminal: in terminal mode it's the program's (⌃\ ⌃N goes to terminal-normal mode);
// in terminal-normal mode it's vim's, [[ and ]] jump between prompts, and i, a, I or A go back.
func (x *terminals) key(c *ui.Ctx, t *terminal, k render.KeyEvent) {
	n := notation(k)
	if t.normal {
		if t.bracket != "" {
			b := t.bracket
			t.bracket = ""
			if n == b {
				if b == "[" {
					t.jumpPrompt(-1)
				} else {
					t.jumpPrompt(1)
				}
				return
			}
			t.vim.Key(b)
		}
		if t.vim.Mode() == "normal" && len(t.vim.Pending()) == 0 {
			switch n {
			case "i", "a", "I", "A":
				t.normal, t.vim, t.ed = false, nil, nil
				t.emu.Scroll(term.Scroll{To: term.ScrollBottom})
				return
			case "[", "]":
				t.bracket = n
				return
			}
		}
		t.vim.Key(n)
		return
	}
	if t.done {
		return
	}
	if n == "<D-v>" || n == "<C-S-v>" {
		if s, ok := c.Clipboard().ReadClipboard(); ok {
			t.pty.Write(t.emu.Paste(s))
		}
		return
	}
	if b := t.emu.EncodeKey(termKey(k)); len(b) > 0 {
		t.emu.Scroll(term.Scroll{To: term.ScrollBottom})
		t.pty.Write(b)
	}
}

// termKeys are the named keys, as render names them, and the physical keys they are.
var termKeys = map[string]term.Key{
	"CR": term.KeyEnter, "Tab": term.KeyTab, "BS": term.KeyBackspace, "Esc": term.KeyEscape, "Del": term.KeyDelete,
	"Up": term.KeyArrowUp, "Down": term.KeyArrowDown, "Left": term.KeyArrowLeft, "Right": term.KeyArrowRight,
	"Home": term.KeyHome, "End": term.KeyEnd, "PageUp": term.KeyPageUp, "PageDown": term.KeyPageDown, "Insert": term.KeyInsert,
	"F1": term.KeyF1, "F2": term.KeyF2, "F3": term.KeyF3, "F4": term.KeyF4, "F5": term.KeyF5, "F6": term.KeyF6,
	"F7": term.KeyF7, "F8": term.KeyF8, "F9": term.KeyF9, "F10": term.KeyF10, "F11": term.KeyF11, "F12": term.KeyF12,
}

// termKey is a key press as the VT's encoder takes it: a physical key, its mods, and the text it types.
func termKey(k render.KeyEvent) term.KeyEvent {
	ev := term.KeyEvent{Action: term.KeyPress}
	if k.Mods&render.ModShift != 0 {
		ev.Mods |= term.ModShift
	}
	if k.Mods&render.ModCtrl != 0 {
		ev.Mods |= term.ModCtrl
	}
	if k.Mods&render.ModAlt != 0 {
		ev.Mods |= term.ModAlt
	}
	if k.Mods&render.ModSuper != 0 {
		ev.Mods |= term.ModSuper
	}
	if key, ok := termKeys[k.Key]; ok {
		ev.Key = key
		return ev
	}
	r, size := utf8.DecodeRuneInString(k.Key)
	if size != len(k.Key) {
		return ev
	}
	lower := r
	if r >= 'A' && r <= 'Z' {
		lower = r + 'a' - 'A'
	}
	switch {
	case lower >= 'a' && lower <= 'z':
		ev.Key = term.KeyA + term.Key(lower-'a')
	case r >= '0' && r <= '9':
		ev.Key = term.KeyDigit0 + term.Key(r-'0')
	case r == ' ':
		ev.Key = term.KeySpace
	}
	ev.Unshifted = lower
	if ev.Mods&(term.ModCtrl|term.ModAlt|term.ModSuper) == 0 {
		ev.Text = k.Key
	}
	return ev
}
