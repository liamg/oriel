package widget

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tCodeSpanBg = theme.DeclareOn("text.code.bg", "alpha(fg, 7%)", "bg.base")
	tCodeSpanFg = theme.Declare("text.code.fg", "fg")
	tStrikeFg   = theme.Declare("text.strike.fg", "mix(red, fg.faint, 35%)")
)

// Span is a run of a Rich paragraph: plain, bold, inline code, or struck through (text an edit took out).
type Span struct {
	Text   string
	Code   bool
	Bold   bool
	Italic bool
	Strike bool
	Tone   render.Color // a code span's colour, on a tint of it: a name in its own colour
}

type RichOpts struct {
	Code   ui.TextStyle // code spans' text: the base's size in mono by default
	Align  ui.Align     // each line's alignment in width: Start, Center or End
	Breaks bool         // a newline in the text starts a new line; otherwise it's a space
}

type richPiece struct {
	text  string
	span  Span
	width float32
	space bool // a space comes before it
	brk   bool // a line break, with no text
}

// richLayout is a paragraph's words, measured: each code span whole; the width of a space; how each span is drawn.
type richLayout struct {
	pieces  []richPiece
	gap     float32
	style   func(Span) ui.TextStyle
	code    ui.TextStyle
	measure func(Span, string) float32
}

// split is p broken into pieces no wider than width, as WrapCode breaks a line: after a space, else anywhere.
func (l richLayout) split(c *ui.Ctx, p richPiece, width float32) []richPiece {
	pad := float32(0)
	if p.span.Code {
		pad = 8
	}
	var out []richPiece
	for i, ln := range codeLines(c.Shaper(), p.text, max(width-pad, 1), l.style(p.span).Font) {
		out = append(out, richPiece{text: ln.text, span: p.span, width: l.measure(p.span, ln.text), space: p.space && i == 0})
	}
	return out
}

func richMeasure(c *ui.Ctx, spans []Span, base ui.TextStyle, o RichOpts) richLayout {
	code := o.Code
	if code.Font.Family == "" {
		code = ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: base.Font.Size - 1}, Color: col(c, tCodeSpanFg)}
	}
	bold := base
	bold.Font.Weight = typeset.SemiBold
	struck := base
	struck.Color = col(c, tStrikeFg)
	style := func(s Span) ui.TextStyle {
		var st ui.TextStyle
		switch {
		case s.Code:
			st = code
		case s.Bold:
			st = bold
		default:
			st = base
		}
		if s.Italic && !s.Code {
			st.Font.Italic = true
		}
		if s.Strike {
			st.Color = struck.Color
		}
		return st
	}
	measure := func(s Span, t string) float32 {
		w := c.Shaper().Shape(t, style(s).Font).Advance
		if s.Code {
			w += 8
		}
		return w
	}
	var pieces []richPiece
	space := false
	for _, s := range spans {
		if s.Code {
			s.Text = strings.ReplaceAll(s.Text, "\n", " ")
			pieces = append(pieces, richPiece{text: s.Text, span: s, width: measure(s, s.Text), space: space})
			space = false
			continue
		}
		lines := []string{s.Text}
		if o.Breaks {
			lines = strings.Split(s.Text, "\n")
		}
		for k, t := range lines {
			if k > 0 {
				pieces = append(pieces, richPiece{brk: true})
				space = false
			}
			lead := strings.HasPrefix(t, " ") || strings.HasPrefix(t, "\n")
			for i, w := range strings.Fields(t) {
				pieces = append(pieces, richPiece{text: w, span: s, width: measure(s, w), space: space || i > 0 || lead})
				space = false
			}
			if strings.TrimSpace(t) == "" && t != "" {
				space = true
			} else if strings.HasSuffix(t, " ") || strings.HasSuffix(t, "\n") {
				space = true
			}
		}
	}
	return richLayout{pieces: pieces, gap: c.Shaper().Shape(" ", base.Font).Advance, style: style, code: code, measure: measure}
}

// widths are the paragraph's width on one line and its widest piece: the narrowest it wraps to.
func (l richLayout) widths() (line, widest float32) {
	var x float32
	for i, p := range l.pieces {
		if p.brk {
			x = 0
			continue
		}
		x += p.width
		if p.space && i > 0 {
			x += l.gap
		}
		line, widest = max(line, x), max(widest, p.width)
	}
	return line, widest
}

// Rich is a paragraph of spans wrapped to width at word boundaries (a code span stays whole unless it's wider than
// a line, when it breaks as WrapCode breaks code; so does a word too wide), each line a row: plain and bold runs in
// base, code in mono on a tint. It returns the number of lines.
func Rich(c *ui.Ctx, key any, spans []Span, width float32, base ui.TextStyle, o RichOpts) int {
	l := richMeasure(c, spans, base, o)
	gap, style, code := l.gap, l.style, l.code
	var lines [][]richPiece
	var joins []string // what a wrapped line joins the one before with, copied
	var x float32
	newLine := func(join string) {
		lines, joins, x = append(lines, nil), append(joins, join), 0
	}
	place := func(p richPiece) {
		add := p.width
		if p.space && x > 0 {
			add += gap
		}
		if len(lines) == 0 || x > 0 && x+add > width {
			join := ""
			if p.space {
				join = " "
			}
			newLine(join)
			add, p.space = p.width, false
		}
		lines[len(lines)-1] = append(lines[len(lines)-1], p)
		x += add
	}
	for _, p := range l.pieces {
		switch {
		case p.brk:
			if len(lines) == 0 {
				newLine("")
			}
			newLine("\n")
		case p.width > width:
			for i, q := range l.split(c, p, width) {
				if i > 0 {
					newLine("")
				}
				place(q)
			}
		default:
			place(p)
		}
	}
	st := ui.Style{Gap: 3, Role: ui.RoleText, Label: richText(spans)}
	if o.Align != ui.Start {
		st.W, st.Cross = ui.Px(width), o.Align
	}
	c.Column(key, st, func(c *ui.Ctx) {
		for i, ln := range lines {
			c.Row(i, ui.Style{Cross: ui.Center}, func(c *ui.Ctx) {
				soft := i > 0
				text := func(c *ui.Ctx, key any, s string, st ui.TextStyle) {
					if soft {
						c.WrappedText(key, s, st, joins[i])
						soft = false
					} else {
						c.Text(key, s, st)
					}
				}
				// Neighbouring words of one span draw as one run; a space between spans is a gap.
				var run strings.Builder
				var cur Span
				n := 0
				flush := func() {
					if run.Len() == 0 {
						return
					}
					st := style(cur)
					if cur.Strike {
						w := c.Shaper().Shape(run.String(), st.Font).Advance
						c.Box(n, ui.Style{}, func(c *ui.Ctx) {
							text(c, "t", run.String(), st)
							c.Box("strike", ui.Style{W: ui.Px(w), H: ui.Px(1), Fill: st.Color, Float: &ui.Float{At: render.Point{Y: .55}}}, nil)
						})
					} else {
						text(c, n, run.String(), st)
					}
					n++
					run.Reset()
				}
				for j, p := range ln {
					sp := p.space && j > 0
					if p.span.Code || p.span != cur {
						flush()
						if sp {
							c.Box(n, ui.Style{W: ui.Px(gap)}, nil)
							n++
						}
						cur = p.span
						if p.span.Code {
							fg, bg := code, col(c, tCodeSpanBg)
							if p.span.Tone.A > 0 {
								fg.Color, bg = p.span.Tone, alpha(p.span.Tone, .14)
							}
							c.Row(n, ui.Style{Pad: ui.PadXY(4, 1), Radius: 3, Fill: bg, Cross: ui.Center}, func(c *ui.Ctx) {
								text(c, "t", p.text, fg)
							})
							n++
							cur = Span{}
							continue
						}
					} else if sp {
						run.WriteString(" ")
					}
					run.WriteString(p.text)
				}
				flush()
			})
		}
	})
	return len(lines)
}

func richText(spans []Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}
