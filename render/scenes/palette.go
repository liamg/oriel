package scenes

import (
	"image/color"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

func hex(v uint32) color.NRGBA {
	return color.NRGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 0xff}
}

func rgba(v uint32, a float32) color.NRGBA {
	c := hex(v)
	c.A = uint8(a*255 + 0.5)
	return c
}

func alpha(c color.NRGBA, a float32) color.NRGBA {
	c.A = uint8(float32(c.A)*a + 0.5)
	return c
}

// Component tokens the reference screens paint with, beyond the semantic ones.
var (
	terminalBg  = theme.Declare("terminal.bg", "#05070a")
	terminalFg  = theme.Declare("terminal.fg", "#c8d2dc")
	threadCard  = theme.Declare("thread.card.bg", "bg.raised")
	gutterAdd   = theme.Declare("gutter.add.fg", "#3c8f6b")
	textVirtual = theme.Declare("text.virtual", "#a08a4a")
	lineCurrent = theme.DeclareOn("line.current", "alpha(#ffffff, 2.5%)", "bg.base")
)

// palette is a theme's colours under the names the mockups' CSS uses.
type palette struct {
	bg0, bg1, bg2, bg3, bg4 color.NRGBA
	line, line2             color.NRGBA
	fg, fg2, fg3, fg4       color.NRGBA

	accent, accentBg, brand2, ink                   color.NRGBA
	green, warn, red, working, violet, pink, ciBlue color.NRGBA
	synK, synS, synN, synF, synT, synC, synP        color.NRGBA
	termFg, termBg                                  color.NRGBA
	card, gutterAdd, vtext, lineCur, diffAdd        color.NRGBA

	glow float32 // the theme's glow strength: 0 turns glows off
	grid float32
}

func paletteOf(r *theme.Resolved) palette {
	syn := func(group string) color.NRGBA { return r.Syntax(group).FG }
	return palette{
		bg0: r.C(theme.BgBase), bg1: r.C(theme.BgSurface), bg2: r.C(theme.BgRaised), bg3: r.C(theme.BgOverlay), bg4: r.C(theme.BgStrong),
		line: r.C(theme.Line), line2: r.C(theme.LineStrong),
		fg: r.C(theme.Fg), fg2: r.C(theme.FgMuted), fg3: r.C(theme.FgFaint), fg4: r.C(theme.FgGhost),
		accent: r.C(theme.Accent), accentBg: r.C(theme.AccentBg), brand2: r.C(theme.Brand), ink: r.C(theme.AccentInk),
		green: r.C(theme.StatusQuiet), warn: r.C(theme.Warn), red: r.C(theme.Error), working: r.C(theme.StatusWorking),
		violet: r.C(theme.StatusPermission), pink: r.C(theme.StatusReviewer), ciBlue: r.C(theme.StatusCI),
		synK: syn("@keyword"), synS: syn("@string"), synN: syn("@number"), synF: syn("@function"), synT: syn("@type"),
		synC: syn("@comment"), synP: syn("@punctuation"),
		termFg: r.C(terminalFg), termBg: r.C(terminalBg),
		card: r.C(threadCard), gutterAdd: r.C(gutterAdd), vtext: r.C(textVirtual), lineCur: r.C(lineCurrent), diffAdd: r.C(theme.DiffAdd),
		glow: float32(r.Effect("glow")), grid: float32(r.Effect("grid")),
	}
}

func mono(size float32, w typeset.Weight, c color.NRGBA) TextStyle {
	return TextStyle{Face: Mono, Weight: w, Size: size, Color: c}
}

func ui(size float32, w typeset.Weight, c color.NRGBA) TextStyle {
	return TextStyle{Face: UI, Weight: w, Size: size, Color: c}
}

// capStyle is the mono micro-label: 600 9.5px, .14em, uppercase.
func (c *Canvas) capStyle() TextStyle {
	return TextStyle{Face: Mono, Weight: typeset.SemiBold, Size: 9.5, Color: c.p.fg3, Spacing: .14}
}

// Builtin resolves Oriel's built-in theme in variant v, which the reference screens are drawn from.
func Builtin(v theme.Variant) (*theme.Resolved, error) {
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		return nil, err
	}
	return theme.Resolve(st, v)
}
