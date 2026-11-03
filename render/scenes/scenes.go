package scenes

import (
	"fmt"
	"image/color"
	"math"
	"strings"

	"github.com/liamg/oriel/render/typeset"
)

// State is what changes between frames.
type State struct {
	Frame int
	T     float64 // seconds since start
	Line  int     // cursor line in the scroll scene
	Typed []rune  // text typed in the type scene

	file  []string
	toks  map[int][]Token
	top   int
	hudAt float64
	hud   [30][]float32
}

// NewState returns the state for frame 0.
func NewState() *State { return &State{toks: map[int][]Token{}} }

// Scene is one thing the spike draws, in CSS pixels.
type Scene interface {
	Name() string
	Size() (w, h int)
	Draw(c *Canvas, st *State)
}

type scene struct {
	name string
	draw func(c *Canvas, st *State)
}

func (s scene) Name() string              { return s.name }
func (s scene) Size() (int, int)          { return IDEWidth, IDEHeight }
func (s scene) Draw(c *Canvas, st *State) { s.draw(c, st) }

// All returns every scene.
func All() []Scene {
	return []Scene{
		scene{"ide", func(c *Canvas, _ *State) { IDE(c) }},
		scene{"scroll", Scroll},
		scene{"text", TextSampler},
		scene{"glow", Glows},
		scene{"hud", HUD},
		scene{"type", Type},
	}
}

// ByName finds a scene.
func ByName(name string) (Scene, bool) {
	for _, s := range All() {
		if s.Name() == name {
			return s, true
		}
	}
	return nil, false
}

// FileLines is the size of the scroll scene's file.
const FileLines = 50_000

// GoFile generates a deterministic n-line Go file.
func GoFile(n int) []string {
	out := make([]string, 0, n)
	out = append(out, "package pricing", "", `import "fmt"`, "")
	for i := 0; len(out) < n; i++ {
		block := []string{
			fmt.Sprintf("// round%d rounds v to %d places, ties to even.", i, i%7),
			fmt.Sprintf("func round%d(v float64, p int32) (float64, error) {", i),
			fmt.Sprintf("\tscaled := v * math.Pow(10, float64(p)) // step %d", i),
			"\tfloor := math.Floor(scaled)",
			"\tswitch diff := scaled - floor; {",
			"\tcase diff > 0.5:",
			"\t\treturn (floor + 1) / math.Pow(10, float64(p)), nil",
			fmt.Sprintf("\tcase diff == 0.5 && int64(floor)%%2 == %d:", i%2),
			fmt.Sprintf(`		return 0, fmt.Errorf("tie at %%v: %d => %%q", v, "even")`, i),
			"\t}",
			"\treturn floor / math.Pow(10, float64(p)), nil",
			"}",
			"",
		}
		out = append(out, block...)
	}
	return out[:n]
}

// Scroll is a 50k-line Go file with the cursor at st.Line, kept on screen like vim with scrolloff=5.
func Scroll(c *Canvas, st *State) {
	if st.file == nil {
		st.file = GoFile(FileLines)
	}
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, c.p.bg0)
	rows := int((IDEHeight - 12) / codeLH)
	const off = 5
	st.Line = max(0, min(st.Line, len(st.file)-1))
	if st.Line < st.top+off {
		st.top = max(0, st.Line-off)
	}
	if st.Line > st.top+rows-1-off {
		st.top = st.Line - rows + 1 + off
	}
	y := float32(6)
	gutter := mono(codeSz, typeset.Normal, c.p.fg4)
	for i := st.top; i < min(st.top+rows, len(st.file)); i++ {
		if i == st.Line {
			c.Fill(Rect{0, y, IDEWidth, codeLH}, c.p.lineCur)
		}
		if i%23 == 4 {
			c.Fill(Rect{0, y, IDEWidth, codeLH}, c.p.diffAdd)
			c.Fill(Rect{62, y, 2, codeLH}, c.p.green)
		}
		num := itoa(i + 1)
		g := gutter
		if i == st.Line {
			g.Color = c.p.fg2
		}
		c.Text(48-c.Measure(num, g), c.Baseline(y+codeLH/2, g), num, g)
		toks, ok := st.toks[i]
		if !ok {
			toks = LexGo(c.p, st.file[i])
			st.toks[i] = toks
		}
		end := c.codeLine(68, y, toks)
		base := c.Baseline(y+codeLH/2, c.codeStyle())
		switch {
		case i%37 == 9:
			c.Wavy(68+8.1*4, end, base+4, c.p.red)
			c.Text(end+22, base, "● undefined: math (compiler)", TextStyle{Face: Mono, Size: 12, Italic: true, Color: alpha(c.p.red, .7)})
		case i%11 == 2:
			c.Text(end+22, base, "← agent · 2m ago", TextStyle{Face: Mono, Size: 12, Italic: true, Color: c.p.fg4})
		}
		if i == st.Line {
			cur := Rect{68, base + 4 - 19, 8.1, 19}
			c.Glow(cur, 10, c.p.accent)
			c.Fill(cur, c.p.accent)
		}
		y += codeLH
	}
}

// TextSampler shows the text-quality cases: ligatures, italics, emoji and CJK
// fallback, hairlines, and code at every size the UI uses.
func TextSampler(c *Canvas, _ *State) {
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, c.p.bg0)
	y := float32(40)
	label := func(s string) {
		c.Text(40, y, strings.ToUpper(s), c.capStyle())
		y += 24
	}
	label("ligatures (JetBrains Mono calt)")
	c.codeLine(40, y-15, LexGo(c.p, `if a != b && c >= d || e <= f { x := y => z; p -> q; i++; s == "==="; www }`))
	y += 30
	label("italic, bold, weights")
	c.Text(40, y, "// italic comment in JetBrains Mono Italic", TextStyle{Face: Mono, Size: codeSz, Italic: true, Color: c.p.synC})
	c.Text(560, y, "Bold mono", mono(codeSz, typeset.Bold, c.p.fg))
	c.Text(720, y, "Inter regular · medium", ui(13, typeset.Normal, c.p.fg))
	c.Text(900, y, "semibold", ui(13, typeset.SemiBold, c.p.fg))
	c.Text(1000, y, "SPACE GROTESK", TextStyle{Face: Display, Weight: typeset.Medium, Size: 16, Color: c.p.fg, Spacing: .2})
	y += 40
	label("fallback: emoji, CJK, symbols (system fonts)")
	c.Text(40, y, "emoji 🙂🚀✅🔥  CJK 漢字かなカナ 한국어  symbols ⌘⌥⇧⌃ ƒ ⎇ ◈ ▷ ⌕ ± ✓ ◐ ○ −∞ →", ui(16, typeset.Normal, c.p.fg))
	y += 40
	label("hairlines: 0.5 px and 1 px, horizontal, vertical, diagonal")
	for i, w := range []float32{0.5, 1} {
		yy := y + float32(i)*20
		c.Line(40, yy, 600, yy, w, c.p.fg2)
		c.Line(640+float32(i)*40, y-10, 640+float32(i)*40, y+30, w, c.p.fg2)
		c.Line(720+float32(i)*80, y-10, 780+float32(i)*80, y+30, w, c.p.fg2)
	}
	y += 60
	label("code sizes")
	for _, sz := range []float32{11, 12, 13, 13.5, 14, 16} {
		t := c.codeStyle()
		t.Size = sz
		c.Text(40, y, fmt.Sprintf("%-5v func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal { // 0O 1lI |", sz), t)
		y += sz * 1.6
	}
	y += 20
	label("ui sizes")
	for _, sz := range []float32{9.5, 10.5, 11.5, 12.5, 13} {
		c.Text(40, y, fmt.Sprintf("%v px  Negative values round the wrong way · round.go:46 · claude working · 1m", sz), ui(sz, typeset.Normal, c.p.fg2))
		y += sz * 1.7
	}
	y += 20
	label("light on dark vs dark on light")
	c.Fill(Rect{40, y - 16, 700, 60}, hex(0xf6f3ea))
	c.codeLine(50, y-12, []Token{{"func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {", hex(0x1b1a17), false}})
	c.codeLine(50, y+12, []Token{{"    return floor.Add(one).Shift(-p) // dark text on light", hex(0x56514a), true}})
	c.codeLine(800, y-12, ideTokens(c.p, "func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {"))
	c.codeLine(800, y+12, ideTokens(c.p, "    return floor.Add(one).Shift(-p) // light text on dark"))
}

// Glows draws the IDE with 50 pulsing glows and an opacity-layer modal over it.
func Glows(c *Canvas, st *State) {
	IDE(c)
	cols := []color.NRGBA{c.p.accent, c.p.working, c.p.red, c.p.violet, c.p.pink, c.p.green, c.p.warn}
	for i := range 50 {
		x := float32(320 + (i%10)*80)
		y := float32(140 + (i/10)*90)
		a := float32(0.55 + 0.45*math.Sin(st.T*4+float64(i)))
		col := cols[i%len(cols)]
		pop := c.Opacity(a)
		c.Glow(Rect{x, y, 40, 14}, 12, col)
		pop()
		c.RRect(Rect{x, y, 40, 14}, 7, col)
	}
	pop := c.Opacity(0.94)
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, rgba(0x030508, .72))
	card := Rect{500, 300, 600, 300}
	c.Glow(card, 30, rgba(0x000000, .8))
	c.RRect(card, 10, c.p.bg2)
	c.Border(card, 10, 1, c.p.line2)
	c.Text(card.X+20, card.Y+36, "Opacity layer over a live editor", ui(16, typeset.SemiBold, c.p.fg))
	c.Text(card.X+20, card.Y+62, "Scrim at .72, card in a .94 opacity layer; 50 glows pulse underneath.", ui(12.5, typeset.Normal, c.p.fg2))
	pop()
}

// HUD draws 30 panes of live readouts, updated at 4 Hz.
func HUD(c *Canvas, st *State) {
	if st.T-st.hudAt >= 0.25 || st.hud[0] == nil {
		st.hudAt = st.T
		for i := range st.hud {
			v := float32(0.5 + 0.45*math.Sin(st.T*0.7+float64(i)*1.3))
			st.hud[i] = append(st.hud[i], v)
			if len(st.hud[i]) > 40 {
				st.hud[i] = st.hud[i][1:]
			}
		}
	}
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, c.p.bg0)
	names := []string{"5H", "WK", "CTX", "CACHE", "CPU", "MEM", "BUS", "CI", "TESTS", "REVIEW"}
	for i := range 30 {
		x := float32(20 + (i%6)*262)
		y := float32(20 + (i/6)*194)
		r := Rect{x, y, 250, 182}
		c.RRect(r, 6, c.p.bg1)
		c.Border(r, 6, 1, c.p.line)
		c.Text(x+12, y+22, names[i%len(names)]+" · "+itoa(i+1), c.capStyle())
		h := st.hud[i]
		v := h[len(h)-1]
		c.Text(x+12, y+62, fmt.Sprintf("%.1f%%", v*100), TextStyle{Face: Display, Weight: typeset.Medium, Size: 28, Color: c.p.fg})
		c.RRect(Rect{x + 12, y + 76, 226, 4}, 2, c.p.bg4)
		c.RRect(Rect{x + 12, y + 76, 226 * v, 4}, 2, c.p.accent)
		c.Bars(x+12, y+168, 5.6, 3.5, 70, h, alpha(c.p.working, .75))
	}
}

// Type is an empty buffer that echoes typed text, for typometer.
func Type(c *Canvas, st *State) {
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, c.p.bg0)
	c.Text(68, 40, "TYPE HERE: KEYPRESS-TO-PHOTON (TYPOMETER)", c.capStyle())
	y := float32(60)
	// No caret, so typometer only sees the dots it types.
	c.codeLine(68, y, []Token{{string(st.Typed), c.p.fg, false}})
	// Typometer takes the line to run until the background changes; this bar ends it inside the window.
	c.Fill(Rect{780, 0, 4, IDEHeight}, c.p.accent)
}
