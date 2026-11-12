package gitui

import (
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/vimreg"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// settings are the editor's options where the window is, read and set as the editor's :set does them.
func (x *Diff) settings() vimreg.Settings {
	return vimreg.Settings{Options: x.h.Options(), At: func(string, string) registry.At { return x.h.Here() }}
}

// wraps says long lines wrap: the editor's 'wrap', where the window is.
func (x *Diff) wraps() bool {
	v, _ := x.settings().Get("wrap", "", "")
	return v == true
}

// setWrap sets 'wrap' as :set wrap does where the window is.
func (x *Diff) setWrap(on bool) error {
	return x.settings().Set("wrap", vim.GlobalOption, "", "", on)
}

// diffPan is a sideways scroll of a file's lines: by columns, or halves of the view.
type diffPan struct {
	file            string
	columns, halves int
}

// rowFit is a line's row's height and how its parts sit: one line high, centred; with 'wrap', as high as its rows,
// the gutters on the first.
func (x *Diff) rowFit() (ui.Size, ui.Align) {
	if x.wrap {
		return ui.Size{}, ui.Start
	}
	return ui.Px(diffLineH), ui.Center
}

// codeFit is how a line of file's code fits its box: wrapped with 'wrap', else scrolled across with the file's other
// lines.
func (x *Diff) codeFit(st *diffState, file string) codeFit {
	if x.wrap {
		return codeFit{wrap: true}
	}
	return codeFit{share: st.shareID(file), width: &st.codeW}
}

// codeFit is how a line's code fits its box: wrap it, or scroll it across with every box naming share, saying its
// width in width.
type codeFit struct {
	wrap  bool
	share ui.ID
	width *float32
}

// shareID is the id a file's lines scroll across by together, old and new sides alike.
func (st *diffState) shareID(file string) ui.ID {
	return ui.Child(ui.Child(0, "diff.across"), diffInstance(st.root, st.head)+"\x00"+file)
}

// panLines scrolls the files' lines across as diff.scroll asked, by columns or halves of a line's code box.
func (st *diffState) panLines(c *ui.Ctx) {
	col := c.Shaper().Shape("0", diffCodeFont).Advance
	for _, p := range st.pan {
		id := st.shareID(p.file)
		d := float32(p.columns)*col + float32(p.halves)*max(st.codeW/2, col)
		c.ScrollTo(id, render.Point{X: max(0, c.Scroll(id).X+d)})
	}
	if len(st.pan) > 0 {
		st.pan = nil
		c.WakeAt(c.Now()) // the files' lines are laid out at their new offset
	}
}

// codePiece is a stretch of a line's text in one colour, marked or not.
type codePiece struct {
	text   []rune
	ts     ui.TextStyle
	marked bool
}

// diffCode is a line's text in its highlight colours, its changed words on a tint of word. With wrap it takes as many
// rows as it needs at its box's width last frame, broken at the column as the editor's 'wrap' does; without, it's
// whole on one row that scrolls across with its file's other lines.
func diffCode(c *ui.Ctx, spans []kit.Span, marks [][2]int, word render.Color, fit codeFit) {
	th := c.Theme()
	var pieces []codePiece
	off := 0
	for _, s := range spans {
		col := th.C(theme.Fg)
		if s.Capture != "" {
			col = th.Syntax("@" + s.Capture).FG
		}
		ts := ui.TextStyle{Font: diffCodeFont, Color: col}
		for pos := 0; pos < len(s.Text); {
			in, end := markAt(marks, off+pos)
			e := min(max(end-off, pos+1), len(s.Text))
			pieces = append(pieces, codePiece{[]rune(kit.ExpandTabs(s.Text[pos:e])), ts, in})
			pos = e
		}
		off += len(s.Text)
	}
	draw := func(c *ui.Ctx, pieces []codePiece) {
		for k, p := range pieces {
			if p.marked {
				c.Box(k, ui.Style{Fill: word, Radius: 2}, func(c *ui.Ctx) { c.Text("t", string(p.text), p.ts) })
			} else {
				c.Text(k, string(p.text), p.ts)
			}
		}
	}
	if !fit.wrap {
		id := c.Next("code")
		if r, ok := c.Rect(id); ok && fit.width != nil {
			*fit.width = r.Dx()
		}
		c.Row("code", ui.Style{W: ui.Flex(1), Cross: ui.Center, ScrollX: true, ScrollWith: fit.share, Bars: ui.BarsHidden}, func(c *ui.Ctx) { draw(c, pieces) })
		return
	}
	cols := 0
	if r, ok := c.Rect(c.Next("code")); ok {
		if adv := c.Shaper().Shape("0", diffCodeFont).Advance; adv > 0 {
			cols = int(r.Dx() / adv)
		}
	}
	rows := breakCols(pieces, cols)
	c.Column("code", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
		for i, row := range rows {
			c.Row(i, ui.Style{H: ui.Px(diffLineH), Cross: ui.Center}, func(c *ui.Ctx) { draw(c, row) })
		}
	})
}

// breakCols cuts pieces into rows of cols columns each, one row if cols is 0.
func breakCols(pieces []codePiece, cols int) [][]codePiece {
	if cols <= 0 {
		return [][]codePiece{pieces}
	}
	var rows [][]codePiece
	var row []codePiece
	used := 0
	for _, p := range pieces {
		for len(p.text) > 0 {
			if used == cols {
				rows, row, used = append(rows, row), nil, 0
			}
			n := min(len(p.text), cols-used)
			row = append(row, codePiece{p.text[:n], p.ts, p.marked})
			used += n
			p.text = p.text[n:]
		}
	}
	return append(rows, row)
}

// markAt says whether byte at is in a mark, and where that stretch (marked or not) ends.
func markAt(marks [][2]int, at int) (bool, int) {
	for _, m := range marks {
		if at < m[0] {
			return false, m[0]
		}
		if at < m[1] {
			return true, m[1]
		}
	}
	return false, 1 << 30
}
