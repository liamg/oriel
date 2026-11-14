package settings

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// inspector is the right column: the chain, the value, contrast and lint.
func (e *ThemeEditor) inspector(c *ui.Ctx, th *theme.Resolved) {
	if e.loaded != e.sel {
		e.load(th)
	}
	c.Column("inspect", ui.Style{W: ui.Px(340), H: ui.Flex(1), Fill: th.C(tThemePanel)}, func(c *ui.Ctx) {
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true}, func(c *ui.Ctx) {
			e.chain(c, th)
			widget.Rule(c, "r1")
			e.value(c, th)
			widget.Rule(c, "r2")
			e.contrast(c, th)
		})
		widget.Rule(c, "r3")
		c.Row("acts", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.Button(c, "reset", "Reset to inherited", widget.ButtonOpts{Help: "shell/theme#toml"}) {
				e.fail(e.reset(e.sel))
			}
		})
	})
}

func (e *ThemeEditor) load(th *theme.Resolved) {
	e.loaded = e.sel
	e.expr = exprOf(th, e.sel)
	col := colorOf(th, e.sel)
	e.hex = theme.Hex(col)
	e.lch = theme.ToOKLCH(col)
	if math.IsNaN(e.lch.H) {
		e.lch.H = 0
	}
}

func kindOf(name string) string {
	return strings.ToUpper(theme.Section(name)[0])
}

func (e *ThemeEditor) chain(c *ui.Ctx, th *theme.Resolved) {
	links := th.Chain(e.picked)
	if len(links) == 0 {
		links = []theme.Link{{Name: e.picked, Expr: exprOf(th, e.picked)}}
	}
	c.Column("chain", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 8}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Text("t", "PICKED", capLabel(th))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", e.picked, kit.MonoFont(10, th.C(tThemePick)))
		})
		for i, l := range links {
			on := l.Name == e.sel
			stroke := th.C(theme.Line)
			if on {
				stroke = th.C(theme.Accent)
			}
			label := strings.TrimPrefix(l.Name, "palette:")
			in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(10, 0), Gap: 10, Cross: ui.Center, Stroke: stroke,
				Border: 1, Radius: 5, Fill: th.C(theme.BgBase), Help: "shell/theme#chain", Role: ui.RoleListItem, Label: label, Selected: on}, func(c *ui.Ctx) {
				c.Box("sw", ui.Style{W: ui.Px(14), H: ui.Px(14), Radius: 3, Fill: colorOf(th, l.Name), Stroke: th.C(theme.LineStrong), Border: 1}, nil)
				c.Text("n", label, kit.MonoFont(11.5, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				kind := kindOf(l.Name)
				if on {
					kind += " · EDIT"
				}
				c.Text("k", kind, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Tracking: .1}, Color: th.C(theme.FgGhost)})
			})
			if in.Clicked {
				e.sel = l.Name
			}
		}
		if n := e.dependants(th, e.sel); n > 0 && e.sel != e.picked {
			widget.Wrap(c, "note", fmt.Sprintf("Editing %s also changes %d other tokens. Pick the top link to change only %s.",
				strings.TrimPrefix(e.sel, "palette:"), n, e.picked), 310, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		}
	})
}

// dependants counts the tokens whose chain runs through name, not counting name itself.
func (e *ThemeEditor) dependants(th *theme.Resolved, name string) int {
	n := 0
	for _, t := range theme.Tokens() {
		if t.Name == name {
			continue
		}
		if slices.ContainsFunc(th.Chain(t.Name), func(l theme.Link) bool { return l.Name == name }) {
			n++
		}
	}
	return n
}

func (e *ThemeEditor) value(c *ui.Ctx, th *theme.Resolved) {
	c.Column("value", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 10}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "VALUE", capLabel(th))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			widget.Segmented(c, "mode", []string{"DERIVE", "FIXED"}, &e.mode, widget.SegmentedOpts{Help: "shell/theme#value"})
		})
		col := colorOf(th, e.sel)
		if e.mode == 0 {
			c.Row("expr", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				if widget.Input(c, "in", &e.expr, widget.InputOpts{Label: "Expression", Help: "shell/theme#value"}).Submitted {
					e.fail(e.set(e.sel, e.expr))
				}
				c.Text("eq", "= "+theme.Hex(col), kit.MonoFont(11, th.C(theme.FgFaint)))
			})
			return
		}
		lch := e.lch
		track := func(f func(t float64) theme.OKLCH) func(float64) render.Color {
			return func(t float64) render.Color { return theme.FromOKLCH(f(t)) }
		}
		sliders := []struct {
			key, label string
			v          *float64
			max, step  float64
			at         func(t float64) theme.OKLCH
			fmt        string
		}{
			{"l", "L", &e.lch.L, 1, 0.005, func(t float64) theme.OKLCH { return theme.OKLCH{L: t, C: lch.C, H: lch.H, A: 1} }, "%.3f"},
			{"c", "C", &e.lch.C, 0.37, 0.002, func(t float64) theme.OKLCH { return theme.OKLCH{L: lch.L, C: t * .37, H: lch.H, A: 1} }, "%.3f"},
			{"h", "H", &e.lch.H, 360, 1, func(t float64) theme.OKLCH { return theme.OKLCH{L: lch.L, C: max(lch.C, .1), H: t * 360, A: 1} }, "%.0f°"},
		}
		changed := false
		for _, s := range sliders {
			c.Row(s.key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("l", s.label, kit.MonoFont(10, th.C(theme.FgFaint)))
				if widget.Slider(c, "s", s.v, widget.SliderOpts{Min: 0, Max: s.max, Step: s.step, Width: ui.Flex(1), Label: s.label,
					Track: track(s.at), Help: "shell/theme#value"}) {
					changed = true
				}
				c.Text("v", fmt.Sprintf(s.fmt, *s.v), kit.MonoFont(11, th.C(theme.Fg)))
			})
		}
		if changed {
			e.lch.A = 1
			e.hex = theme.Hex(theme.FromOKLCH(e.lch))
			e.fail(e.setting(e.sel, fmt.Sprintf("oklch(%.3f %.3f %.1f)", e.lch.L, e.lch.C, e.lch.H), true))
		}
		c.Row("hex", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sw", ui.Style{W: ui.Px(24), H: ui.Px(24), Radius: 4, Fill: col, Stroke: th.C(theme.LineStrong), Border: 1}, nil)
			if widget.Input(c, "in", &e.hex, widget.InputOpts{Label: "Hex", Width: ui.Px(120), Help: "shell/theme#value"}).Submitted {
				e.fail(e.set(e.sel, e.hex))
				e.loaded = ""
			}
		})
	})
}

func (e *ThemeEditor) contrast(c *ui.Ctx, th *theme.Resolved) {
	if e.lintTh != th {
		e.lint, e.lintTh = theme.Lint(th), th
	}
	c.Column("contrast", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 10}, func(c *ui.Ctx) {
		c.Text("t", "CONTRAST", capLabel(th))
		var pairs []theme.Pair
		for _, p := range theme.Pairs() {
			if p.FG == e.sel || p.On == e.sel {
				pairs = append(pairs, p)
			}
		}
		if len(pairs) == 0 {
			c.Text("none", "No declared pairs for "+strings.TrimPrefix(e.sel, "palette:")+".", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgFaint)})
		}
		for row := 0; row*3 < len(pairs); row++ {
			c.Row(row, ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				for i := row * 3; i < min(len(pairs), row*3+3); i++ {
					p := pairs[i]
					fg, _ := th.Color(p.FG)
					bg, _ := th.Color(p.On)
					ratio := theme.ContrastRatio(fg, bg)
					tone := theme.OK
					if ratio < p.Min {
						tone = theme.Warn
					}
					other := p.On
					if p.On == e.sel {
						other = p.FG
					}
					c.Column(i, ui.Style{W: ui.Flex(1), Pad: ui.Pad(8), Gap: 4, Stroke: th.C(theme.Line), Border: 1, Radius: 5, Fill: bg,
						Role: ui.RoleText, Label: fmt.Sprintf("%s on %s: %.1f to 1", p.FG, p.On, ratio)}, func(c *ui.Ctx) {
						c.Text("o", other, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: fg})
						c.Text("r", fmt.Sprintf("%.1f:1", ratio), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 14, Weight: typeset.SemiBold}, Color: th.C(tone)})
					})
				}
			})
		}
		for i, f := range e.lint {
			c.Column(fmt.Sprint("lint", i), ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
				widget.Wrap(c, "m", "⚠ "+f.Message, 310, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.FgMuted)})
				if f.Fix != "" && widget.Link(c, "fix", "Fix: "+f.Token+" = "+f.Fix, "shell/theme#contrast") {
					e.fail(e.set(f.Token, f.Fix))
				}
			})
		}
	})
}
