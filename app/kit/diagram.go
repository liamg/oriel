package kit

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/diagram"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// diagramFonts are what a mermaid diagram is set in.
var diagramFonts = diagram.Fonts{
	Label: typeset.FontSpec{Family: typeset.UI, Size: 13},
	Small: typeset.FontSpec{Family: typeset.UI, Size: 11.5},
	Title: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14},
}

type diagramKey struct {
	src string
	sh  typeset.Shaper
}

type diagramLaid struct {
	l   *diagram.Layout
	err error
}

// diagrams are a UI's mermaid sources parsed and laid out, once per source and shaper, the most recently drawn kept.
type diagrams struct {
	*Memo[diagramKey, diagramLaid]
}

// DiagramOf is a mermaid source laid out, or why it isn't drawn (a *diagram.UnsupportedError or *diagram.SyntaxError).
func DiagramOf(c *ui.Ctx, src string) (*diagram.Layout, error) {
	sh := c.Shaper()
	cache := ui.Keep(c, func() diagrams { return diagrams{NewMemo[diagramKey, diagramLaid](128)} })
	k := diagramKey{src, sh}
	laid := cache.Get(k, func() diagramLaid {
		d, err := diagram.Parse(k.src)
		if err != nil {
			return diagramLaid{err: err}
		}
		return diagramLaid{l: d.Layout(sh, diagramFonts)}
	})
	return laid.l, laid.err
}

// DiagramLabel is what a diagram is called for a screen reader and in the lightbox: its kind and its nodes.
func DiagramLabel(l *diagram.Layout) string {
	var names []string
	for _, n := range l.Nodes {
		names = append(names, strings.ReplaceAll(n.Label, "\n", " "))
	}
	kind := "Flowchart"
	if l.Kind == "sequence" {
		kind = "Sequence diagram"
	}
	return kind + ": " + strings.Join(names, ", ")
}

// DiagramKicker is the label over a drawn diagram, as 05-plan has it: "MERMAID · FLOWCHART".
func DiagramKicker(l *diagram.Layout) string {
	return "MERMAID · " + strings.ToUpper(l.Kind)
}

// Diagram draws a laid-out diagram centred in width and scaled down to fit it, zoomable in o's lightbox group.
func Diagram(c *ui.Ctx, key any, l *diagram.Layout, width float32, o MarkdownOpts) {
	th := c.Theme()
	pal := diagram.ThemePalette(th)
	v := widget.Vector{Size: l.Size, Fill: th.C(theme.BgSurface), Draw: func(f render.Frame, r render.Rect) { l.Draw(f, r, pal) }}
	for _, tr := range l.Truncated {
		v.Tips = append(v.Tips, widget.VectorTip{Rect: tr.Rect, Text: tr.Text})
	}
	c.Column(key, ui.Style{W: ui.Px(width), Cross: ui.Center}, func(c *ui.Ctx) {
		widget.VectorImage(c, "d", v, min(l.Size.X, width), widget.ImageOpts{Label: DiagramLabel(l), Help: o.Help, Zoom: true, Group: o.Group})
	})
}
