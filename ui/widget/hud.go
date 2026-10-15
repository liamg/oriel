package widget

import (
	"fmt"
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The HUD's look (ui.md 10): a drawer of framed panels, and the indicators in every slot.
var (
	tIndicatorLabel   = theme.Declare("indicator.label", "fg.faint")
	tIndicatorValue   = theme.Declare("indicator.value", "fg")
	tIndicatorBar     = theme.Declare("indicator.bar", "accent")
	tPanelBg          = theme.DeclareOn("hud.panel.bg", "alpha(bg.raised, 55%)", "bg.base")
	tPanelLine        = theme.Declare("hud.panel.line", "line")
	tPanelCorner      = theme.Declare("hud.panel.corner", "alpha(accent, 60%)")
	tPanelTitle       = theme.Declare("hud.panel.title", "accent")
	tPanelNote        = theme.Declare("hud.panel.note", "fg.ghost")
	tDrawerBg         = theme.Declare("hud.bg", "alpha(bg.base, 97%)")
	tDrawerEdge       = theme.Declare("hud.edge", "alpha(accent, 35%)")
	tDrawerGlow       = theme.Declare("hud.edge.glow", "accent")
	tDrawerGrid       = theme.DeclareOn("hud.grid", "alpha(fg, 3%)", "bg.base")
	tDrawerGrab       = theme.Declare("hud.grab", "accent")
	tMeterProjection  = theme.DeclareOn("meter.projection", "alpha(accent, 45%)", "bg.surface")
	tMeterProjectMark = theme.Declare("meter.projection.mark", "fg.muted")
)

type IndicatorOpts struct {
	Label string
	// Meter draws a small bar filled to Frac (0–1) and its share; Warn turns it to the warning colour from there on.
	Meter bool
	Frac  float64
	Warn  float64
	Value string      // a figure after the label or the bar: "4", "$31.40"
	Tone  color.NRGBA // the bar's and the value's colour; the indicator's own when zero
	Dot   color.NRGBA // a status dot before the value, when set
	Help  string
}

// Indicator is a tiny live readout, as the title bar, the status line and the HUD draw them: a label, then a bar and
// its share, then a value.
func Indicator(c *ui.Ctx, key any, o IndicatorOpts) ui.ID {
	bar, value := col(c, tIndicatorBar), col(c, tIndicatorValue)
	if o.Tone != (color.NRGBA{}) {
		bar, value = o.Tone, o.Tone
	}
	if o.Meter && o.Warn > 0 && o.Frac >= o.Warn {
		bar, value = col(c, theme.Warn), col(c, theme.Warn)
	}
	label := o.Label
	if o.Meter {
		label += fmt.Sprintf(" %d%%", int(o.Frac*100+.5))
	}
	if o.Value != "" {
		label += " " + o.Value
	}
	return c.Row(key, ui.Style{Gap: 6, Cross: ui.Center, Role: ui.RoleStatus, Label: strings.TrimSpace(label), Help: o.Help, Keep: true}, func(c *ui.Ctx) {
		if o.Label != "" {
			c.Text("label", o.Label, monoText(10.5, typeset.Medium, col(c, tIndicatorLabel), 0))
		}
		if o.Meter {
			Meter(c, "bar", o.Frac, MeterOpts{Tone: bar, Width: ui.Px(44), Label: o.Label})
			c.Text("pct", fmt.Sprintf("%d%%", int(o.Frac*100+.5)), monoText(10.5, typeset.SemiBold, col(c, tIndicatorValue), 0))
		}
		if o.Dot != (color.NRGBA{}) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: o.Dot}, nil)
		}
		if o.Value != "" {
			c.Text("value", o.Value, monoText(10.5, typeset.SemiBold, value, 0))
		}
	})
}

type PanelOpts struct {
	Title string // in caps, in the accent
	Note  string // dim, at the heading's right
	Help  string
	W, H  ui.Size // fills by default
}

// Panel is one of the HUD's instruments: a hairline frame with corner brackets, its title and note across the top,
// and body under them.
func Panel(c *ui.Ctx, key any, o PanelOpts, body func(c *ui.Ctx)) ui.ID {
	w, h := o.W, o.H
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	if h == (ui.Size{}) {
		h = ui.Flex(1)
	}
	corner := col(c, tPanelCorner)
	return c.Column(key, ui.Style{W: w, H: h, Pad: ui.PadXY(12, 10), Gap: 9, Fill: col(c, tPanelBg), Stroke: col(c, tPanelLine), Border: 1, Clip: true,
		Role: ui.RoleGroup, Label: o.Title, Help: o.Help}, func(c *ui.Ctx) {
		c.Box("brackets", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Paint("b", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
				const n = 8
				var p render.Path
				p.MoveTo(render.Point{X: r.Min.X, Y: r.Min.Y + n}).LineTo(r.Min).LineTo(render.Point{X: r.Min.X + n, Y: r.Min.Y})
				p.MoveTo(render.Point{X: r.Max.X - n, Y: r.Max.Y}).LineTo(r.Max).LineTo(render.Point{X: r.Max.X, Y: r.Max.Y - n})
				f.Path(&p, render.Style{Stroke: corner, Width: 1})
			})
		})
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("title", strings.ToUpper(o.Title), monoText(9.5, typeset.SemiBold, col(c, tPanelTitle), .14))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if o.Note != "" {
				c.Text("note", o.Note, monoText(10, typeset.Normal, col(c, tPanelNote), 0))
			}
		})
		body(c)
	})
}

type DrawerOpts struct {
	Top     float32 // where it hangs from: under the title bar
	Pinned  bool    // it stays until closed: it takes focus, and its grab says how to close it
	Grab    string  // the tab under its edge: "⌥ RELEASE TO HIDE · CLICK TO PIN"
	Label   string
	Help    string
	KeyMode string
}

// DrawerResult is what happened to a drawer this frame.
type DrawerResult struct {
	Pressed bool // the pointer went down on it, or on anything in it
	Grabbed bool // its grab tab was clicked
}

// Drawer is a sheet pulled down over the window from Top, as wide as it, as tall as body: the HUD. A pinned one
// holds keyboard focus while nothing in it has it; Escape there or on something in it closes it (KeyMode's keys).
func Drawer(c *ui.Ctx, key any, o DrawerOpts, body func(c *ui.Ctx)) DrawerResult {
	id := c.Next(key)
	res := DrawerResult{Pressed: c.PressedWithin(id)}
	size := c.Size()
	at := render.Point{Y: o.Top}
	edge, glow, grid := col(c, tDrawerEdge), col(c, tDrawerGlow), col(c, tDrawerGrid)
	c.Column(key, ui.Style{W: ui.Px(size.X), Pad: ui.Insets{L: 16, T: 14, R: 16, B: 14}, Gap: 12, Fill: col(c, tDrawerBg), Z: ui.LayerDialog,
		Float: &ui.Float{Point: &at}, Interactive: true, NoHover: true, Focusable: o.Pinned, KeyMode: o.KeyMode, Role: ui.RoleDialog, Label: o.Label,
		Help: o.Help, Enter: &ui.Transition{Shift: render.Point{Y: -24}, Fade: true}, Exit: &ui.Transition{Shift: render.Point{Y: -24}, Fade: true}},
		func(c *ui.Ctx) {
			if o.Pinned && !c.FocusWithin(id) {
				c.Focus(id)
			}
			c.Box("grid", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1), Z: ui.LayerUnder}, func(c *ui.Ctx) {
				c.Paint("lines", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
					var p render.Path
					for x := r.Min.X + 24; x < r.Max.X; x += 24 {
						p.MoveTo(render.Point{X: x, Y: r.Min.Y}).LineTo(render.Point{X: x, Y: r.Max.Y})
					}
					for y := r.Min.Y + 24; y < r.Max.Y; y += 24 {
						p.MoveTo(render.Point{X: r.Min.X, Y: y}).LineTo(render.Point{X: r.Max.X, Y: y})
					}
					f.Path(&p, render.Style{Stroke: grid, Width: 1})
					f.Rect(render.Rect{Min: render.Point{X: r.Min.X, Y: r.Max.Y - 1}, Max: r.Max}, render.Style{Fill: edge})
					mid := r.Min.X + r.Dx()/2
					line := render.Rect{Min: render.Point{X: mid - 210, Y: r.Max.Y - 1}, Max: render.Point{X: mid + 210, Y: r.Max.Y + 1}}
					f.Effect(render.Glow, line, 10, theme.WithAlpha(glow, .5))
					f.Rect(line, render.Style{Fill: glow})
				})
			})
			body(c)
			if o.Grab == "" {
				return
			}
			grab := render.Point{X: .5, Y: 0}
			in := Clickable(c, "grab", ui.Style{Float: &ui.Float{At: render.Point{X: .5, Y: 1}, Self: grab}, Pad: ui.PadXY(10, 4), Fill: col(c, tDrawerBg),
				Stroke: edge, Border: 1, Radius: 6, Label: o.Grab, Help: o.Help}, func(c *ui.Ctx) {
				c.Text("t", o.Grab, monoText(9.5, typeset.SemiBold, col(c, tDrawerGrab), .14))
			})
			res.Grabbed = in.Clicked
		})
	return res
}
