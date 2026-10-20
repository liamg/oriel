package app

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/diagram"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// mdDiagramFonts are what a markdown diagram is set in.
var mdDiagramFonts = diagram.Fonts{
	Label: typeset.FontSpec{Family: typeset.UI, Size: 13},
	Small: typeset.FontSpec{Family: typeset.UI, Size: 11.5},
	Title: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14},
}

type mdDiagramKey struct {
	src string
	sh  typeset.Shaper
}

type mdDiagramLaid struct {
	l   *diagram.Layout
	err error
}

// mdDiagrams are the mermaid fences parsed and laid out, once per source and shaper.
var mdDiagrams = struct {
	sync.Mutex
	m map[mdDiagramKey]mdDiagramLaid
}{m: map[mdDiagramKey]mdDiagramLaid{}}

func isMermaid(b mdBlock) bool {
	f := strings.Fields(b.Lang)
	return b.Kind == mdCode && len(f) > 0 && strings.EqualFold(f[0], "mermaid")
}

// mdDiagramOf is a mermaid fence's diagram laid out, or why it isn't drawn.
func mdDiagramOf(sh typeset.Shaper, b mdBlock) (*diagram.Layout, error) {
	k := mdDiagramKey{strings.Join(b.Code, "\n"), sh}
	mdDiagrams.Lock()
	defer mdDiagrams.Unlock()
	if d, ok := mdDiagrams.m[k]; ok {
		return d.l, d.err
	}
	var laid mdDiagramLaid
	d, err := diagram.Parse(k.src)
	if err != nil {
		laid.err = err
	} else {
		laid.l = d.Layout(sh, mdDiagramFonts)
	}
	if len(mdDiagrams.m) >= 64 {
		clear(mdDiagrams.m)
	}
	mdDiagrams.m[k] = laid
	return laid.l, laid.err
}

// mdFenceKicker is a fence's label over its code: its language, and for a mermaid fence drawn as code, why.
func mdFenceKicker(c *ui.Ctx, b mdBlock) string {
	k := strings.ToUpper(b.Lang)
	if !isMermaid(b) {
		return k
	}
	_, err := mdDiagramOf(c.Shaper(), b)
	var un *diagram.UnsupportedError
	var syn *diagram.SyntaxError
	switch {
	case errors.As(err, &un):
		return k + " · " + strings.ToUpper(un.Type) + " ISN'T DRAWN"
	case errors.As(err, &syn):
		return fmt.Sprintf("%s · CAN'T READ LINE %d", k, syn.Line)
	}
	return k
}

// mdDiagramLabel is what a diagram is called for a screen reader and in the lightbox: its kind and its nodes.
func mdDiagramLabel(l *diagram.Layout) string {
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

// mdDiagramKicker is the label over a drawn diagram, as 05-plan has it: "MERMAID · FLOWCHART".
func mdDiagramKicker(l *diagram.Layout) string {
	return "MERMAID · " + strings.ToUpper(l.Kind)
}

// mdDiagram draws a laid-out diagram centred in width and scaled down to fit it, zoomable in the document's lightbox
// group.
func mdDiagram(c *ui.Ctx, key any, l *diagram.Layout, width float32, o mdOpts) {
	th := c.Theme()
	pal := diagram.ThemePalette(th)
	v := widget.Vector{Size: l.Size, Fill: th.C(theme.BgSurface), Draw: func(f render.Frame, r render.Rect) { l.Draw(f, r, pal) }}
	for _, tr := range l.Truncated {
		v.Tips = append(v.Tips, widget.VectorTip{Rect: tr.Rect, Text: tr.Text})
	}
	c.Column(key, ui.Style{W: ui.Px(width), Cross: ui.Center}, func(c *ui.Ctx) {
		widget.VectorImage(c, "d", v, min(l.Size.X, width), widget.ImageOpts{Label: mdDiagramLabel(l), Help: o.Help, Zoom: true, Group: o.Group})
	})
}

// mdDiagramBox is mdDiagram in a fence's box: a document's, with the kicker over it, or the compact one threads and
// tickets draw. It returns false for a fence it doesn't draw.
func mdDiagramBox(c *ui.Ctx, key any, b mdBlock, width float32, o mdOpts, doc bool) bool {
	if !isMermaid(b) {
		return false
	}
	l, _ := mdDiagramOf(c.Shaper(), b)
	if l == nil {
		return false
	}
	th := c.Theme()
	if !doc {
		c.Column(key, ui.Style{W: ui.Px(width), Pad: ui.Pad(8), Radius: 4, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
			mdDiagram(c, "d", l, width-16, o)
		})
		return true
	}
	c.Column(key, ui.Style{W: ui.Px(width), Pad: ui.Pad(14), Gap: 12, Radius: 6, Stroke: th.C(tMdCodeBorder), Border: 1, Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Text("lang", mdDiagramKicker(l), kickerFont(th.C(theme.FgFaint)))
		mdDiagram(c, "d", l, width-28, o)
	})
	return true
}
