package app

import (
	"context"
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// decorations are the decorations of the lines [top, bottom) of a pane's buffer, by line, each line's in priority
// order. A provider that panics is turned off, as any contribution is.
func (x *editorExt) decorations(p vim.Pane, top, bottom int) map[int][]registry.Decoration {
	snap := p.Buf()
	path := p.Name()
	if !filepath.IsAbs(path) {
		path = filepath.Join(x.dir, path)
	}
	ctx := registry.DecorationCtx{Name: p.Name(), Path: path, Buffer: p.Buffer(), Snap: snap, Top: top, Bottom: bottom, FileBytes: p.FileBytes}
	off := map[string]bool{}
	for _, o := range x.h.TurnedOff() {
		if o.Kind == registry.KindDecoration {
			off[o.ID] = true
		}
	}
	out := map[int][]registry.Decoration{}
	for _, prov := range x.h.DecorationProviders() {
		if off[prov.ID] {
			continue
		}
		var ds []registry.Decoration
		if err := registry.Call(func() error { ds = prov.Decorations(ctx); return nil }); err != nil {
			x.h.TurnOff(registry.KindDecoration, prov.ID, err)
			continue
		}
		for _, d := range ds {
			if d.Anchor != 0 {
				o, ok := p.Buffer().Resolve(d.Anchor)
				if !ok {
					continue
				}
				d.Line = snap.PosAt(min(o, snap.Len())).Line
			}
			if d.Line >= top && d.Line < bottom {
				out[d.Line] = append(out[d.Line], d)
			}
		}
	}
	for _, ds := range out {
		slices.SortStableFunc(ds, func(a, b registry.Decoration) int { return a.Priority - b.Priority })
	}
	return out
}

// tokenColour is a decoration's Style as a colour: a theme token or palette colour, or a syntax group.
func tokenColour(th *theme.Resolved, name string, fallback color.NRGBA) color.NRGBA {
	if name == "" {
		return fallback
	}
	if name[0] == '@' {
		return th.Syntax(name).FG
	}
	if c, ok := th.Color(name); ok {
		return c
	}
	return fallback
}

// lineBg is the background a line's LineBg decoration gives it, and its gutter edge.
func lineBg(th *theme.Resolved, ds []registry.Decoration) (bg, edge color.NRGBA) {
	for _, d := range ds {
		if d.Kind == registry.LineBg {
			bg = tokenColour(th, d.Style, bg)
			edge = tokenColour(th, d.Edge, edge)
		}
	}
	return bg, edge
}

// signs draws a line's gutter signs, between its number and its text; one with a command runs it when clicked.
func (x *editorExt) signs(c *ui.Ctx, th *theme.Resolved, ds []registry.Decoration, gw float32, m paneMetrics) {
	c.Row("signs", ui.Style{W: ui.Px(14), H: ui.Flex(1), Cross: ui.Center, Main: ui.Center, Float: &ui.Float{Offset: render.Point{X: gw - 16}}}, func(c *ui.Ctx) {
		for i, d := range ds {
			if d.Kind != registry.Sign {
				continue
			}
			col := tokenColour(th, d.Style, th.C(theme.FgMuted))
			draw := func(c *ui.Ctx) {
				if d.Text == "" {
					c.Box("box", ui.Style{W: ui.Px(12), H: ui.Px(12), Radius: 3, Fill: col}, nil)
					return
				}
				if w, h, ok := blockPart(d.Text); ok {
					c.Box("cell", ui.Style{W: ui.Px(m.charW), H: ui.Px(m.lineH)}, func(c *ui.Ctx) {
						c.Box("part", ui.Style{W: ui.Px(w * m.charW), H: ui.Px(h * m.lineH), Fill: col,
							Float: &ui.Float{Offset: render.Point{Y: (1 - h) * m.lineH}}}, nil)
					})
					return
				}
				c.Text("t", d.Text, ui.TextStyle{Font: m.font, Color: col})
			}
			if d.Command == "" {
				c.Box(i, ui.Style{}, draw)
				continue
			}
			tip := d.Tooltip
			if tip == "" {
				tip = d.Command
			}
			if widget.Clickable(c, i, ui.Style{Radius: 3, Label: tip, Tooltip: tip, Help: "editor/pane"}, draw).Clicked {
				if err := x.h.Run(context.Background(), d.Command, d.Args); err != nil {
					x.h.Notify(core.Notice{ID: "editor.sign", Severity: core.SevWarning, Key: true, Text: err.Error()})
				}
			}
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
func virtualText(c *ui.Ctx, th *theme.Resolved, ds []registry.Decoration) {
	for i, d := range ds {
		if d.Kind != registry.VirtualText {
			continue
		}
		c.Box(fmt.Sprint("vgap", i), ui.Style{W: ui.Px(22)}, nil)
		c.Text(fmt.Sprint("vt", i), d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Italic: true},
			Color: tokenColour(th, d.Style, th.C(theme.FgGhost))})
	}
}

// spans draws a line's Highlight and Underline decorations over its text, x0 being where the text starts.
func spans(c *ui.Ctx, th *theme.Resolved, p vim.Pane, line int, ds []registry.Decoration, rowID ui.ID, x0 float32, m paneMetrics) {
	for i, d := range ds {
		if d.Kind != registry.Highlight && d.Kind != registry.Underline {
			continue
		}
		a, z := p.VCol(line, d.From)+inlayShift(ds, d.From, true), p.VCol(line, d.To)+inlayShift(ds, d.To, false)
		if z <= a {
			continue
		}
		col := tokenColour(th, d.Style, th.C(theme.Accent))
		x, w := x0+float32(a)*m.charW, float32(z-a)*m.charW
		if d.Kind == registry.Highlight {
			c.Box(fmt.Sprint("hl", i), ui.Style{W: ui.Px(w), H: ui.Px(m.lineH), Fill: theme.WithAlpha(col, .22), Z: ui.LayerContent + 1,
				Float: &ui.Float{To: rowID, Offset: render.Point{X: x}}}, nil)
			continue
		}
		c.Box(fmt.Sprint("ul", i), ui.Style{W: ui.Px(w), H: ui.Px(4), Z: ui.LayerContent + 1, Float: &ui.Float{To: rowID,
			Offset: render.Point{X: x, Y: m.lineH - 5}}}, func(c *ui.Ctx) {
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

// blocks draws a line's Block decorations under it, from where its text starts, and returns their ids and whether
// each wants revealing.
func blocks(c *ui.Ctx, line int, ds []registry.Decoration, x0 float32) (ids []ui.ID, reveal []bool) {
	for _, d := range ds {
		if d.Kind != registry.Block || d.Node == nil {
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
func inlays(ds []registry.Decoration) []registry.Decoration {
	var out []registry.Decoration
	for _, d := range ds {
		if d.Kind == registry.Inlay {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b registry.Decoration) int { return a.From - b.From })
	return out
}

// inlayCells is how many character cells an inlay takes: its text's.
func inlayCells(d registry.Decoration) int { return utf8.RuneCountInString(d.Text) }

// inlayShift is how many cells the inlays before column col (and at it, with at) push it right.
func inlayShift(ds []registry.Decoration, col int, at bool) int {
	n := 0
	for _, d := range ds {
		if d.Kind == registry.Inlay && (d.From < col || at && d.From == col) {
			n += inlayCells(d)
		}
	}
	return n
}

// inlayBox draws an inlay hint inline, exactly as wide as its cells so the code after it stays on the grid.
func inlayBox(c *ui.Ctx, key any, d registry.Decoration, m paneMetrics) {
	th := c.Theme()
	c.Box(key, ui.Style{W: ui.Px(float32(inlayCells(d)) * m.charW), Cross: ui.Center, Main: ui.Center, Fill: th.C(tInlayBg), Radius: 3}, func(c *ui.Ctx) {
		c.Text("t", d.Text, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: m.font.Size - 2}, Color: tokenColour(th, d.Style, th.C(tInlay))})
	})
}
