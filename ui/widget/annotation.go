package widget

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The annotation house style (ui.md: explain, :Inspect): a numbered diamond, an angled leader line, and a
// clipped-corner card.
var (
	tAnnotationBg    = theme.Declare("annotation.bg", "bg.surface")
	tAnnotationFg    = theme.Declare("annotation.fg", "fg.muted")
	tAnnotationIndex = theme.Declare("annotation.index.fg", "bg.base")
)

// annotationCut is how much of the card's top-right and bottom-left corners is cut away.
const annotationCut = 14

type AnnotationOpts struct {
	N      string // in the diamond: "1"
	Title  string // beside the diamond, in caps and the tone
	Tone   color.NRGBA
	Width  ui.Size
	Ground color.NRGBA // what's behind the card, which its cut corners show; the base background by default
	Help   string
	Label  string // its accessible name; the title by default
}

// Annotation is a note in the annotation house style: its number in a diamond and its title in the tone, over body,
// in a card with two corners cut. It returns the card's id, for a leader line to it.
func Annotation(c *ui.Ctx, key any, o AnnotationOpts, body func(c *ui.Ctx)) ui.ID {
	w := o.Width
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	ground := o.Ground
	if ground.A == 0 {
		ground = col(c, theme.BgBase)
	}
	border := alpha(o.Tone, .45)
	return c.Column(key, ui.Style{W: w, Pad: ui.Insets{L: 14, T: 11, R: 14, B: 12}, Gap: 5, Fill: col(c, tAnnotationBg), Stroke: border, Border: 1,
		Radius: 2, Role: ui.RoleGroup, Label: cmpStr(o.Label, o.Title), Help: o.Help}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			Diamond(c, "n", o.N, 18, o.Tone, col(c, tAnnotationIndex))
			c.Text("t", strings.ToUpper(o.Title), monoText(10, typeset.SemiBold, o.Tone, .12))
		})
		if body != nil {
			body(c)
		}
		corner := func(k string, at render.Point, top bool) {
			c.Box(k, ui.Style{Float: &ui.Float{At: at, Self: at}, Z: ui.LayerContent + 1}, func(c *ui.Ctx) {
				c.Paint("cut", ui.Px(annotationCut), ui.Px(annotationCut), func(f render.Frame, r render.Rect) {
					var p render.Path
					if top {
						p.MoveTo(r.Min).LineTo(render.Point{X: r.Max.X, Y: r.Min.Y}).LineTo(r.Max).Close()
					} else {
						p.MoveTo(r.Min).LineTo(render.Point{X: r.Min.X, Y: r.Max.Y}).LineTo(r.Max).Close()
					}
					f.Path(&p, render.Style{Fill: ground})
					var edge render.Path
					edge.MoveTo(r.Min).LineTo(r.Max)
					f.Path(&edge, render.Style{Stroke: border, Width: 1})
				})
			})
		}
		corner("cut-tr", render.Point{X: 1}, true)
		corner("cut-bl", render.Point{Y: 1}, false)
	})
}

// AnnotationBody is an annotation's text style: what a body passed to Annotation draws its prose in.
func AnnotationBody(c *ui.Ctx) ui.TextStyle {
	return uiText(c, 12.5, typeset.Normal, col(c, tAnnotationFg))
}

// Diamond is label in a diamond of size filled with fill: the annotation house style's marker, and a step's.
func Diamond(c *ui.Ctx, key any, label string, size float32, fill, fg color.NRGBA) ui.ID {
	return c.Box(key, ui.Style{W: ui.Px(size), H: ui.Px(size), Main: ui.Center, Cross: ui.Center, Keep: true, Role: ui.RoleText, Label: label},
		func(c *ui.Ctx) {
			c.Box("under", ui.Style{Float: &ui.Float{}, Z: ui.LayerUnder}, func(c *ui.Ctx) {
				c.Paint("shape", ui.Px(size), ui.Px(size), func(f render.Frame, r render.Rect) {
					f.Path(DiamondPath(r), render.Style{Fill: fill})
				})
			})
			if label != "" {
				c.Text("n", label, monoText(size*.55, typeset.Bold, fg, 0))
			}
		})
}

// DiamondPath is the diamond inscribed in r.
func DiamondPath(r render.Rect) *render.Path {
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	var p render.Path
	p.MoveTo(render.Point{X: cx, Y: r.Min.Y}).LineTo(render.Point{X: r.Max.X, Y: cy}).LineTo(render.Point{X: cx, Y: r.Max.Y}).
		LineTo(render.Point{X: r.Min.X, Y: cy}).Close()
	return &p
}

// LeaderPath is an annotation's leader line from the code at from to its card at to: level from from, then angled up
// or down to to's height over 40 px, then level into to.
func LeaderPath(from, to render.Point) *render.Path {
	knee := max(from.X+12, to.X-110)
	bend := min(knee+40, to.X)
	var p render.Path
	p.MoveTo(from).LineTo(render.Point{X: knee, Y: from.Y}).LineTo(render.Point{X: bend, Y: to.Y}).LineTo(to)
	return &p
}

// Leader draws a leader line from from to to in tone, with a dot where it starts.
func Leader(f render.Frame, from, to render.Point, tone color.NRGBA) {
	f.Path(LeaderPath(from, to), render.Style{Stroke: alpha(tone, .8), Width: 1})
	f.Path(render.Ellipse(render.Rect{Min: render.Point{X: from.X - 3, Y: from.Y - 3}, Max: render.Point{X: from.X + 3, Y: from.Y + 3}}),
		render.Style{Fill: tone})
}
