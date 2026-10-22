package editor

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

// decorations are the decorations of the lines [top, bottom) of a pane's buffer, by line, each line's in priority
// order; wake, if not nil, is asked for a frame when a provider wants to be asked again.
func (x *Editor) decorations(p vim.Pane, top, bottom int, wake func(time.Time)) map[int][]uireg.Decoration {
	path := p.Name()
	if !filepath.IsAbs(path) {
		path = filepath.Join(x.dir, path)
	}
	return x.h.Decorate(uireg.DecorationCtx{Window: strconv.Itoa(int(p.ID())), Now: x.now, Name: p.Name(), Path: path, Buffer: p.Buffer(),
		Snap: p.Buf(), Top: top, Bottom: bottom, FileBytes: p.FileBytes, Wake: wake})
}

// lineBg is the background a line's LineBg decoration gives it, and its gutter edge.
func lineBg(th *theme.Resolved, ds []uireg.Decoration) (bg, edge color.NRGBA) {
	for _, d := range ds {
		if d.Kind == uireg.LineBg {
			bg = kit.TokenColour(th, d.Style, bg)
			edge = kit.TokenColour(th, d.Edge, edge)
		}
	}
	return bg, edge
}

// signColW is the width of the gutter's sign column, and signBarW what its right edge keeps for block signs.
const (
	signColW = 14
	signBarW = 3
)

// signs draws a line's gutter signs, between its number and its text. A block sign (the git bar) has a fixed place
// at the column's right edge, so a run of them lines up whatever else a line has; of the rest, the one with the
// highest priority sits centred left of it, the others in its menu (uireg.TopSign).
func (x *Editor) signs(c *ui.Ctx, ds []uireg.Decoration, gw float32, m kit.PaneMetrics) {
	c.Row("signs", ui.Style{W: ui.Px(signColW), H: ui.Flex(1), Pad: ui.Insets{R: signBarW}, Cross: ui.Center, Main: ui.Center,
		Float: &ui.Float{Offset: render.Point{X: gw - 16}}}, func(c *ui.Ctx) {
		var room [2]uireg.Decoration
		rest := room[:0]
		for i, d := range ds {
			if d.Kind != uireg.Sign {
				continue
			}
			if w, h, ok := blockPart(d.Text); ok && d.Icon == "" && d.Command == "" && d.Hover == nil && len(d.Menu) == 0 {
				col := kit.TokenColour(c.Theme(), d.Style, c.Theme().C(theme.FgMuted))
				c.Box(i, ui.Style{W: ui.Px(w * m.CharW), H: ui.Px(h * m.LineH), Fill: col,
					Float: &ui.Float{Offset: render.Point{X: signColW - w*m.CharW, Y: (1 - h) * m.LineH}}}, nil)
				continue
			}
			rest = append(rest, d)
		}
		if d, ok := uireg.TopSign(rest); ok {
			kit.DrawSign(c, x.h, "sign", d, m.Font.Size)
		}
	})
}

// blockPart is the part of a line's cell a lower (▁…█) or left (▉…▏) block element fills, so a run of them on
// consecutive lines joins up, whatever the font's line height.
func blockPart(s string) (w, h float32, ok bool) {
	r, n := utf8.DecodeRuneInString(s)
	switch {
	case n != len(s):
		return 0, 0, false
	case r >= 0x2581 && r <= 0x2588:
		return 1, float32(r-0x2580) / 8, true
	case r >= 0x2589 && r <= 0x258f:
		return float32(0x2590-r) / 8, 1, true
	}
	return 0, 0, false
}

// virtualText draws a line's virtual text after its end.
func virtualText(c *ui.Ctx, th *theme.Resolved, ds []uireg.Decoration) {
	for i, d := range ds {
		if d.Kind != uireg.VirtualText {
			continue
		}
		c.Box(fmt.Sprint("vgap", i), ui.Style{W: ui.Px(22)}, nil)
		if d.Bg != "" {
			fg := kit.TokenColour(th, d.Style, th.C(theme.Accent))
			c.Row(fmt.Sprint("vt", i), ui.Style{Pad: ui.PadXY(7, 1), Fill: kit.TokenColour(th, d.Bg, th.C(theme.BgRaised)), Stroke: theme.WithAlpha(fg, .35),
				Border: 1, Radius: 3, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5}, Color: fg})
			})
			continue
		}
		c.Text(fmt.Sprint("vt", i), d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Italic: true},
			Color: kit.TokenColour(th, d.Style, th.C(theme.FgGhost))})
	}
}

// spans draws a line's Highlight and Underline decorations over its text, x0 being where the text starts.
func spans(c *ui.Ctx, th *theme.Resolved, p vim.Pane, line int, ds []uireg.Decoration, rowID ui.ID, x0 float32, m kit.PaneMetrics, segs []rowSeg) {
	for i, d := range ds {
		if d.Kind != uireg.Highlight && d.Kind != uireg.Underline {
			continue
		}
		a, z := p.VCol(line, d.From)+inlayShift(ds, d.From, true), p.VCol(line, d.To)+inlayShift(ds, d.To, false)
		col := kit.TokenColour(th, d.Style, th.C(theme.Accent))
		for r, g := range segs {
			dx, w, ok := g.clip(a, z, m)
			if !ok {
				continue
			}
			x := x0 + dx
			if d.Kind == uireg.Highlight {
				c.Box(fmt.Sprint("hl", i, ".", r), ui.Style{W: ui.Px(w), H: ui.Px(m.LineH), Fill: theme.WithAlpha(col, .22), Z: ui.LayerContent + 1,
					Float: &ui.Float{To: rowID, Offset: render.Point{X: x, Y: g.y}}}, nil)
				continue
			}
			c.Box(fmt.Sprint("ul", i, ".", r), ui.Style{W: ui.Px(w), H: ui.Px(4), Z: ui.LayerContent + 1, Float: &ui.Float{To: rowID,
				Offset: render.Point{X: x, Y: g.y + m.LineH - 5}}}, func(c *ui.Ctx) {
				c.Paint("wave", ui.Px(w), ui.Px(4), func(f render.Frame, r render.Rect) {
					var path render.Path
					path.MoveTo(render.Point{X: r.Min.X, Y: r.Min.Y + 3})
					for px, up := r.Min.X, true; px < r.Max.X; px, up = px+2, !up {
						y := r.Min.Y + 3
						if up {
							y = r.Min.Y + 1
						}
						path.LineTo(render.Point{X: min(px+2, r.Max.X), Y: y})
					}
					f.Path(&path, render.Style{Stroke: col, Width: 1})
				})
			})
		}
	}
}

// blocks draws a line's Block decorations under it, from where its text starts, and returns their ids and whether
// each wants revealing.
func blocks(c *ui.Ctx, line int, ds []uireg.Decoration, x0 float32) (ids []ui.ID, reveal []bool) {
	for _, d := range ds {
		if d.Kind != uireg.Block || d.Node == nil {
			continue
		}
		// Keyed by its place among the line's blocks, not among all its decorations, so what has focus in it keeps it
		// as signs and highlights come and go.
		ids = append(ids, c.Row(fmt.Sprint("block", line, ".", len(ids)), ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: x0, T: 4, B: 6, R: 20}}, func(c *ui.Ctx) {
			c.Node(d.Node)
		}))
		reveal = append(reveal, d.Reveal)
	}
	return ids, reveal
}

var tInlay = theme.Declare("editor.inlay", "fg.ghost")
var tInlayBg = theme.DeclareOn("editor.inlay.bg", "alpha(fg, 5%)", "bg.base")

// inlays are a line's Inlay decorations, by column.
func inlays(ds []uireg.Decoration) []uireg.Decoration {
	var out []uireg.Decoration
	for _, d := range ds {
		if d.Kind == uireg.Inlay {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b uireg.Decoration) int { return a.From - b.From })
	return out
}

// inlayCells is how many character cells an inlay takes: its text's.
func inlayCells(d uireg.Decoration) int { return utf8.RuneCountInString(d.Text) }

// inlayShift is how many cells the inlays before column col (and at it, with at) push it right.
func inlayShift(ds []uireg.Decoration, col int, at bool) int {
	n := 0
	for _, d := range ds {
		if d.Kind == uireg.Inlay && (d.From < col || at && d.From == col) {
			n += inlayCells(d)
		}
	}
	return n
}

// inlayBox draws an inlay hint inline, exactly as wide as its cells so the code after it stays on the grid.
func inlayBox(c *ui.Ctx, key any, d uireg.Decoration, m kit.PaneMetrics) {
	th := c.Theme()
	c.Box(key, ui.Style{W: ui.Px(float32(inlayCells(d)) * m.CharW), Cross: ui.Center, Main: ui.Center, Fill: th.C(tInlayBg), Radius: 3}, func(c *ui.Ctx) {
		c.Text("t", d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: m.Font.Size - 2}, Color: kit.TokenColour(th, d.Style, th.C(tInlay))})
	})
}
