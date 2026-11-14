package editor

import (
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit/diagrams"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render/diagram"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func isMermaid(b mdBlock) bool {
	f := strings.Fields(b.Lang)
	return b.Kind == mdCode && len(f) > 0 && strings.EqualFold(f[0], "mermaid")
}

// mdDiagramOf is a mermaid fence's diagram laid out, or why it isn't drawn.
func mdDiagramOf(c *ui.Ctx, b mdBlock) (*diagram.Layout, error) {
	return diagrams.Of(c, strings.Join(b.Code, "\n"))
}

// mdFenceKicker is a fence's label over its code: its language, and for a mermaid fence drawn as code, why.
func mdFenceKicker(c *ui.Ctx, b mdBlock) string {
	k := strings.ToUpper(b.Lang)
	if !isMermaid(b) {
		return k
	}
	_, err := mdDiagramOf(c, b)
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

// mdDiagramBox is mdDiagram in a fence's box: a document's, with the kicker over it, or the compact one threads and
// tickets draw. It returns false for a fence it doesn't draw.
func mdDiagramBox(c *ui.Ctx, key any, b mdBlock, width float32, o mdOpts, doc bool) bool {
	if !isMermaid(b) {
		return false
	}
	l, _ := mdDiagramOf(c, b)
	if l == nil {
		return false
	}
	th := c.Theme()
	if !doc {
		c.Column(key, ui.Style{W: ui.Px(width), Pad: ui.Pad(8), Radius: 4, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
			diagrams.Draw(c, "d", l, width-16, o)
		})
		return true
	}
	c.Column(key, ui.Style{W: ui.Px(width), Pad: ui.Pad(14), Gap: 12, Radius: 6, Stroke: th.C(tMdCodeBorder), Border: 1, Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Text("lang", diagrams.Kicker(l), kit.KickerFont(th.C(theme.FgFaint)))
		diagrams.Draw(c, "d", l, width-28, o)
	})
	return true
}
