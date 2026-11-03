package scenes

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render/typeset"
)

// The 01-ide mockup is 1600×1000 CSS px; positions below are read off its HTML and base.css.
const (
	IDEWidth, IDEHeight = 1600, 1000

	titleH  = 38
	statusH = 26
	railW   = 44
	sideW   = 250
	rightW  = 380
	lowerH  = 200
	codeLH  = 21
	codeSz  = 13.5
)

func (c *Canvas) codeStyle() TextStyle {
	return TextStyle{Face: Mono, Weight: typeset.Normal, Size: codeSz, Color: c.p.fg}
}

// codeLine draws coloured tokens from x on the line whose top is top, and returns the end x.
func (c *Canvas) codeLine(x, top float32, toks []Token) float32 {
	base := c.Baseline(top+codeLH/2, c.codeStyle())
	for _, tk := range toks {
		t := c.codeStyle()
		t.Color, t.Italic = tk.Color, tk.Italic
		x += c.Text(x, base, strings.ReplaceAll(tk.Text, "\t", "    "), t)
	}
	return x
}

// IDE draws the 01-ide mockup.
func IDE(c *Canvas) {
	c.Fill(Rect{0, 0, IDEWidth, IDEHeight}, c.p.bg0)
	mainTop, mainBot := float32(titleH), float32(IDEHeight-statusH)
	c.titlebar()
	c.rail(mainTop, mainBot)
	c.side(railW, mainTop, mainBot)
	centerX := float32(railW + sideW)
	centerW := float32(IDEWidth - railW - sideW - rightW)
	c.editor(centerX, mainTop, centerW, mainBot-lowerH)
	c.lower(centerX, mainBot-lowerH, centerW)
	c.rightRail(IDEWidth-rightW, mainTop, mainBot)
	c.statusline(mainBot)
}

func (c *Canvas) logo(x, cy, size float32) {
	s := size / 100
	// The SVG mark: a ring and a bar with the same top and bottom (28 to 78 of 100), under
	// drop-shadow(0 0 4px). The bar isn't snapped to pixels, so it stays level with the ring.
	cursor := Rect{x + 68*s, cy + (28-50)*s, 13 * s, 50 * s}
	c.GlowDisc(x+38*s, cy+3*s, 25*s, 4, rgba(0xffaa28, .22))
	c.Glow(cursor, 4, rgba(0xffaa28, .3))
	c.Ring(x+38*s, cy+(53-50)*s, 21*s, 8*s, c.p.accent)
	c.FillFree(cursor, c.p.brand2)
}

func (c *Canvas) titlebar() {
	c.Fill(Rect{0, 0, IDEWidth, titleH}, c.p.bg1)
	c.Fill(Rect{0, titleH - 1, IDEWidth, 1}, c.p.line)
	cy := float32(titleH-1) / 2

	brandStyle := TextStyle{Face: Display, Weight: typeset.Bold, Size: 12, Color: c.p.fg, Spacing: .2}
	end := row(14, cy, 8, item{22, func(x, cy float32) { c.logo(x, cy, 22) }}, c.txt("ORIEL", brandStyle))
	// Trailing letter-spacing is part of the box in CSS.
	end += 14
	c.Fill(Rect{end, 0, 1, titleH - 1}, c.p.line)
	x := end + 1

	type stab struct {
		dot        color.NRGBA
		name, mini string
		key        string
	}
	tabs := []stab{
		{c.p.working, "pricing-rounding", "plan", "⌘1"},
		{c.p.red, "ENG-2231 search api", "needs you", "⌘2"},
		{c.p.violet, "review #4410", "permission", "⌘3"},
		{c.p.ciBlue, "flaky e2e sweep", "ci", "⌘4"},
		{c.p.pink, "dash-filters", "in review", "⌘5"},
		{c.p.green, "auth flow", "", "⌘6"},
	}
	for i, t := range tabs {
		col := c.p.fg2
		if i == 0 {
			col = c.p.fg
		}
		items := []item{c.dot(t.dot, 8), c.txt(t.name, ui(12, typeset.Normal, col))}
		if t.mini != "" {
			items = append(items, c.txt(t.mini, mono(10, typeset.Medium, c.p.fg3)))
		}
		items = append(items, c.txt(t.key, mono(9.5, typeset.Medium, c.p.fg4)))
		w := width(items, 7) + 24
		if i == 0 {
			c.Fill(Rect{x, 0, w, titleH - 1}, c.p.bg2)
			c.Glow(Rect{x, 0, w, 2}, 10, c.p.accent)
			c.Fill(Rect{x, 0, w, 2}, c.p.accent)
		}
		row(x+12, cy, 7, items...)
		c.Fill(Rect{x + w, 0, 1, titleH - 1}, c.p.line)
		x += w + 1
	}

	indicator := mono(10.5, typeset.Medium, c.p.fg2)
	b := mono(10.5, typeset.Medium, c.p.fg)
	right := []item{
		c.txt("5H", indicator), gap(6), c.bar(.38, c.p.accent), gap(6), c.txt("38%", b), gap(14),
		c.txt("WK", indicator), gap(6), c.bar(.71, c.p.warn), gap(6), c.txt("71%", b), gap(14),
		c.dot(c.p.green, 5), gap(6), c.txt("BUS 4", indicator), gap(14), c.kbd("⌘K"),
	}
	start := rowEnd(IDEWidth-14, cy, 0, right...)
	c.Fill(Rect{start - 14 - 1, 0, 1, titleH - 1}, c.p.line)

	// The accent hairline under the titlebar: transparent → accent at 30% → transparent at 70%, opacity .25.
	c.Gradient(Rect{0, titleH - 1, IDEWidth * .3, 1}, true, alpha(c.p.accent, 0), alpha(c.p.accent, .25))
	c.Gradient(Rect{IDEWidth * .3, titleH - 1, IDEWidth * .4, 1}, true, alpha(c.p.accent, .25), alpha(c.p.accent, 0))
}

func (c *Canvas) kbd(s string) item {
	t := mono(10, typeset.Medium, c.p.fg2)
	w := c.Measure(s, t) + 10 + 2
	return item{w, func(x, cy float32) {
		r := Rect{x, cy - 9, w, 18}
		c.RRect(r, 4, c.p.bg2)
		c.Border(r, 4, 1, c.p.line2)
		c.Fill(Rect{x + 1, cy + 8, w - 2, 1}, c.p.line2)
		c.TextMid(x+6, cy-.5, s, t)
	}}
}

func (c *Canvas) rail(top, bot float32) {
	c.Fill(Rect{0, top, railW, bot - top}, c.p.bg1)
	c.Fill(Rect{railW - 1, top, 1, bot - top}, c.p.line)
	icons := []struct {
		g     string
		badge string
	}{{"⌥", ""}, {"⌕", ""}, {"±", "7"}, {"◈", "2"}, {"▷", ""}, {"⎇", ""}}
	y := top + 8
	for i, ic := range icons {
		col := c.p.fg3
		if i == 0 {
			col = c.p.accent
			c.RRect(Rect{6, y, 32, 32}, 7, c.p.accentBg)
			c.RRect(Rect{0, y + 7, 2, 18}, 1, c.p.accent)
		}
		c.railIcon(ic.g, y, col)
		if ic.badge != "" {
			t := mono(8.5, typeset.Bold, hex(0x111111))
			w := max(13, c.Measure(ic.badge, t)+6)
			c.RRect(Rect{6 + 32 - 2 - w, y + 3, w, 13}, 7, c.p.warn)
			c.TextMid(6+32-2-w+(w-c.Measure(ic.badge, t))/2, y+3+6.5, ic.badge, t)
		}
		y += 36
	}
	c.railIcon("⚙", bot-32, c.p.fg3)
}

func (c *Canvas) railIcon(g string, y float32, col color.NRGBA) {
	t := mono(13, typeset.SemiBold, col)
	c.TextMid(6+(32-c.Measure(g, t))/2, y+16, g, t)
}

type treeRow struct {
	depth  int
	caret  string
	sw     color.NRGBA
	name   string
	mark   string
	on     bool
	symCol color.NRGBA
}

func (c *Canvas) side(x, top, bot float32) {
	c.Fill(Rect{x, top, sideW, bot - top}, c.p.bg1)
	c.Fill(Rect{x + sideW - 1, top, 1, bot - top}, c.p.line)
	c.paneHeader(x, top, sideW-1, "Explorer", "pricing · rethink/rounding")

	dir, gof, md, mod := hex(0x5cd2ff), hex(0x4fc1e9), hex(0xffcf5c), hex(0x9aa6b4)
	rows := []treeRow{
		{0, "▾", dir, "internal", "", false, color.NRGBA{}},
		{1, "▾", dir, "pricing", "", false, color.NRGBA{}},
		{2, "", gof, "round.go", "M", true, color.NRGBA{}},
		{2, "", gof, "round_test.go", "M", false, color.NRGBA{}},
		{2, "", gof, "tiers.go", "", false, color.NRGBA{}},
		{2, "", gof, "currency.go", "A", false, color.NRGBA{}},
		{1, "▸", dir, "usage", "", false, color.NRGBA{}},
		{1, "▸", dir, "billing", "", false, color.NRGBA{}},
		{0, "▸", dir, "cmd", "", false, color.NRGBA{}},
		{0, "", md, "PLAN.md", "A", false, color.NRGBA{}},
		{0, "", mod, "go.mod", "", false, color.NRGBA{}},
	}
	y := top + 30 + 4
	for _, r := range rows {
		c.treeRow(x, y, r, ui(12.5, typeset.Normal, c.p.fg2))
		y += 22
	}

	// Outline, pinned to the bottom.
	oTop := bot - 96 - 30
	c.Fill(Rect{x, oTop, sideW - 1, 1}, c.p.line)
	c.paneHeader(x, oTop, sideW-1, "Outline", "")
	outline := []struct {
		sym  string
		col  color.NRGBA
		name string
		on   bool
	}{{"ƒ", c.p.synF, "RoundMinor", false}, {"ƒ", c.p.synF, "roundHalfEven", true}, {"T", c.p.synT, "Mode", false}, {"c", c.p.synN, "defaultPrecision", false}}
	y = oTop + 30 + 4
	for _, o := range outline {
		col := c.p.fg2
		if o.on {
			col = c.p.fg
			c.Fill(Rect{x, y, sideW - 1, 22}, c.p.accentBg)
			c.Fill(Rect{x, y, 2, 22}, c.p.accent)
		}
		t := mono(11.5, typeset.Normal, col)
		sym := t
		sym.Color = o.col
		row(x+12, y+11, 6, c.txt(o.sym, sym), c.txt(o.name, t))
		y += 22
	}
}

func (c *Canvas) treeRow(x, y float32, r treeRow, t TextStyle) {
	if r.on {
		c.Fill(Rect{x, y, sideW - 1, 22}, c.p.accentBg)
		c.Fill(Rect{x, y, 2, 22}, c.p.accent)
		t.Color = c.p.fg
	}
	var items []item
	if r.caret != "" {
		items = append(items, c.txt(r.caret, t))
	}
	items = append(items, item{10, func(x, cy float32) { c.RRect(Rect{x, cy - 5, 10, 10}, 2, alpha(r.sw, .85)) }}, c.txt(r.name, t))
	row(x+12+float32(r.depth)*14, y+11, 6, items...)
	if r.mark != "" {
		col := map[string]color.NRGBA{"M": c.p.warn, "A": c.p.green, "D": c.p.red}[r.mark]
		rowEnd(x+sideW-1-10, y+11, 0, c.txt(r.mark, mono(10, typeset.SemiBold, col)))
	}
}

// paneHeader is the 30 px .ph strip with a cap label and an optional right-hand note.
func (c *Canvas) paneHeader(x, top, w float32, label, note string) {
	c.Fill(Rect{x, top + 29, w, 1}, c.p.line)
	cy := top + 15
	row(x+10, cy, 0, c.txt(strings.ToUpper(label), c.capStyle()))
	if note != "" {
		rowEnd(x+w-10, cy, 0, c.txt(note, mono(10, typeset.Normal, c.p.fg3)))
	}
}

var ideCode = []struct {
	n    int
	src  string
	kind string // "", "add"
}{
	{38, "// roundHalfEven rounds v to p decimal places, ties to even.", ""},
	{39, "func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {", ""},
	{40, "    scaled := v.Shift(p)", ""},
	{41, "    floor := scaled.Floor()", ""},
	{42, "    diff := scaled.Sub(floor)", "add"},
	{43, "    switch diff.Cmp(half) {", "add"},
	{44, "    case 1:", "add"},
	{45, "        return floor.Add(one).Shift(-p)", "add"},
	{46, "    case 0:", "add"},
	{47, "        if floor.IntPart()%2 == 0 { return floor.Shift(-p) }", "add"},
	{48, "        return floor.Add(one).Shift(-p)", "add"},
	{49, "    }", ""},
	{50, "    return floor.Shift(-p)", ""},
	{51, "}", ""},
}

func ideTokens(p palette, src string) []Token {
	toks := LexGo(p, src)
	for i, tk := range toks {
		// The mockup colours qualified types (decimal.Decimal) as types.
		if tk.Text == "decimal" && i+2 < len(toks) && toks[i+1].Text == "." {
			toks[i].Color, toks[i+1].Color, toks[i+2].Color = p.synT, p.synT, p.synT
		}
	}
	return toks
}

func (c *Canvas) editor(x, top, w, bot float32) {
	// Editor tabs.
	c.Fill(Rect{x, top, w, 32}, c.p.bg1)
	c.Fill(Rect{x, top + 31, w, 1}, c.p.line)
	tabs := []struct {
		name  string
		sw    color.NRGBA
		dirty bool
	}{{"round.go", hex(0x4fc1e9), true}, {"round_test.go", hex(0x4fc1e9), false}, {"PLAN.md", hex(0xffcf5c), false}}
	tx := x
	for i, t := range tabs {
		col := c.p.fg3
		if i == 0 {
			col = c.p.fg
		}
		items := []item{{10, func(x, cy float32) { c.RRect(Rect{x, cy - 5, 10, 10}, 2, alpha(t.sw, .85)) }}, c.txt(t.name, ui(12, typeset.Normal, col))}
		if t.dirty {
			items = append(items, c.txt("●", ui(12, typeset.Normal, c.p.fg3)))
		}
		tw := width(items, 7) + 28
		if i == 0 {
			c.Fill(Rect{tx, top, tw, 32}, c.p.bg0)
			c.Fill(Rect{tx, top, tw, 2}, c.p.violet)
		}
		row(tx+14, top+16, 7, items...)
		c.Fill(Rect{tx + tw, top, 1, 31}, c.p.line)
		tx += tw + 1
	}

	// Breadcrumbs.
	ct := top + 32
	c.Fill(Rect{x, ct, w, 26}, c.p.bg0)
	c.Fill(Rect{x, ct + 25, w, 1}, c.p.line)
	crumb := mono(11, typeset.Normal, c.p.fg3)
	fn := crumb
	fn.Color = c.p.synF
	row(x+14, ct+12.5, 6, c.txt("internal › pricing › round.go ›", crumb), c.txt("roundHalfEven", fn))
	rowEnd(x+w-14, ct+12.5, 12, c.chip("1 OPEN THREAD", c.p.working), c.chip("GOPLS ✓", c.p.accent))

	// Code.
	codeTop := ct + 26
	c.Fill(Rect{x, codeTop, w, bot - codeTop}, c.p.bg0)
	done := c.Clip(Rect{x, codeTop, w, bot - codeTop})
	defer done()
	y := codeTop + 6
	gutterR := x + 64 - 16
	textX := x + 64 + 4
	lineW := w - 70
	for _, l := range ideCode {
		gcol := c.p.fg4
		if l.kind == "add" {
			c.Fill(Rect{x, y, lineW, codeLH}, c.p.diffAdd)
			c.Fill(Rect{x + 62, y, 2, codeLH}, c.p.green)
			gcol = c.p.gutterAdd
		}
		num := itoa(l.n)
		nt := mono(codeSz, typeset.Normal, gcol)
		c.Text(gutterR-c.Measure(num, nt), c.Baseline(y+codeLH/2, nt), num, nt)
		end := c.codeLine(textX, y, ideTokens(c.p, l.src))
		switch l.n {
		case 42:
			vt := TextStyle{Face: Mono, Size: 12, Italic: true, Color: c.p.fg4}
			c.Text(end+22, c.Baseline(y+codeLH/2, c.codeStyle()), "← agent · 2m ago", vt)
		case 46:
			// Thread marker in the gutter, then the block cursor.
			c.Glow(Rect{x + 44, y + 4, 12, 12}, 8, rgba(0x4cc9f0, .6))
			c.RRect(Rect{x + 44, y + 4, 12, 12}, 3, c.p.working)
			base := c.Baseline(y+codeLH/2, c.codeStyle())
			cur := Rect{end, base + 4 - 19, 8.1, 19}
			c.Glow(cur, 10, c.p.accent)
			c.Fill(cur, c.p.accent)
			y += codeLH
			y = c.inlineThread(x, y, w)
			continue
		case 50:
			ind := c.Measure("    return ", c.codeStyle())
			fw := c.Measure("floor", c.codeStyle())
			base := c.Baseline(y+codeLH/2, c.codeStyle())
			c.Wavy(textX+ind, textX+ind+fw, base+4, c.p.warn)
			vt := TextStyle{Face: Mono, Size: 12, Italic: true, Color: c.p.vtext}
			c.Text(end+22, base, "● S1023: redundant return (staticcheck)", vt)
		}
		y += codeLH
	}
	c.minimap(x+w-70, codeTop, bot)
}

func (c *Canvas) inlineThread(x, y, w float32) float32 {
	card := Rect{x + 68, y + 4, w - 68 - 90, 0}
	th := float32(36)
	msgUI := ui(12.5, typeset.Normal, c.p.fg2)
	lh := float32(12.5 * 1.45)
	m1 := 9 + 16.7 + 2 + lh + 9
	m2 := 16.7 + 2 + 2*lh + 9
	card.H = th + m1 + m2
	c.RRect(card, 8, c.p.card)
	c.Border(card, 8, 1, c.p.line2)
	// Corner brackets.
	br := alpha(c.p.accent, .55)
	c.Fill(Rect{card.X - 1, card.Y - 1, 9, 1}, br)
	c.Fill(Rect{card.X - 1, card.Y - 1, 1, 9}, br)
	c.Fill(Rect{card.X + card.W - 8, card.Y + card.H, 9, 1}, br)
	c.Fill(Rect{card.X + card.W, card.Y + card.H - 8, 1, 9}, br)

	cy := card.Y + th/2
	head := ui(11.5, typeset.Normal, c.p.fg2)
	row(card.X+10, cy, 8, c.dot(c.p.working, 8), c.txt("Thread", ui(11.5, typeset.Bold, c.p.fg)), c.txt("· L46 ·", head), c.chip("CLAUDE · WORKING", c.p.accent))
	rowEnd(card.X+card.W-10, cy, 0, c.txt("t:7d45", mono(11.5, typeset.Normal, c.p.fg3)))
	c.Fill(Rect{card.X + 1, card.Y + th - 1, card.W - 2, 1}, c.p.line)

	my := card.Y + th + 9
	c.message(card.X+10, my, "LG", hex(0xffcf5c), hex(0xff9b45), "liam", "1m", [][]span2{
		{{"ties go to even — but what about negative values? ", false}, {"-2.5", true}, {" should give ", false}, {"-2", true}, {", and Floor goes the wrong way.", false}},
	}, msgUI)
	my += m1
	c.message(card.X+10, my, "C", hex(0xff9b6b), hex(0xd9774a), "Claude", "now", [][]span2{
		{{"Right — Floor rounds toward −∞. Switching to ", false}, {"Truncate", true}, {" with sign handling and adding the negative cases to", false}},
		{{"TestRoundHalfEven", true}, {". ", false}, {"running go test ./internal/pricing…", false}},
	}, msgUI)
	return card.Y + card.H + 6
}

type span2 struct {
	s    string
	code bool
}

func (c *Canvas) message(x, y float32, initials string, g1, g2 color.NRGBA, who, when string, lines [][]span2, body TextStyle) {
	av := Rect{x, y, 22, 22}
	c.avatar(av, g1, g2, initials)
	tx := x + 22 + 9
	cy := y + 16.7/2
	row(tx, cy, 6, c.txt(who, ui(11.5, typeset.SemiBold, c.p.fg)), c.txt(when, mono(10, typeset.Normal, c.p.fg4)))
	ly := y + 16.7 + 2
	lh := float32(12.5 * 1.45)
	code := mono(11.5, typeset.Normal, c.p.synP)
	for li, l := range lines {
		lx := tx
		mid := ly + lh/2
		for si, s := range l {
			if s.code {
				w := c.Measure(s.s, code) + 8
				c.RRect(Rect{lx, mid - 8, w, 16}, 3, c.p.bg4)
				c.TextMid(lx+4, mid, s.s, code)
				lx += w
				continue
			}
			t := body
			if li == len(lines)-1 && si == len(l)-1 && strings.HasPrefix(s.s, "running") {
				t.Color = c.p.fg3
			}
			lx += c.Text(lx, c.Baseline(mid, body), s.s, t)
		}
		ly += lh
	}
}

func (c *Canvas) avatar(r Rect, g1, g2 color.NRGBA, s string) {
	done := c.ClipRound(r, 6)
	c.Gradient(r, true, g1, g2)
	done()
	t := mono(10, typeset.Bold, hex(0x0b0f15))
	c.TextMid(r.X+(r.W-c.Measure(s, t))/2, r.Y+r.H/2, s, t)
}

func (c *Canvas) minimap(x, top, bot float32) {
	c.Fill(Rect{x, top, 70, bot - top}, c.p.bg0)
	c.Fill(Rect{x, top, 1, bot - top}, c.p.line)
	widths := []float32{30, 44, 20, 38, 26, 40, 18, 32, 36, 12, 30, 44, 20, 38, 20}
	for i, w := range widths {
		col := alpha(c.p.fg4, .5)
		switch {
		case i >= 4 && i <= 6:
			col = alpha(c.p.green, .5)
		case i == 7:
			col = alpha(c.p.warn, .5)
		}
		c.RRect(Rect{x + 1 + 8, top + 2 + float32(i)*6, w, 2}, 1, col)
	}
	c.Fill(Rect{x + 1, top + 20, 69, 90}, alpha(c.p.accent, .06))
	c.Fill(Rect{x + 1, top + 20, 69, 1}, alpha(c.p.accent, .3))
	c.Fill(Rect{x + 1, top + 109, 69, 1}, alpha(c.p.accent, .3))
}

func (c *Canvas) lower(x, top, w float32) {
	c.Fill(Rect{x, top, w, lowerH}, c.p.bg1)
	c.Fill(Rect{x, top, w, 1}, c.p.line)
	tabs := []string{"AGENT ▸ CLAUDE", "TERMINAL", "PROBLEMS 1", "DEBUG", "TESTS"}
	tt := TextStyle{Face: Mono, Weight: typeset.SemiBold, Size: 10, Color: c.p.fg3, Spacing: .1}
	tx := x + 12
	cy := top + 1 + 14
	for i, s := range tabs {
		t := tt
		tw := c.Measure(s, t)
		if i == 0 {
			t.Color = c.p.fg
			c.Fill(Rect{tx, top + 1 + 26, tw, 2}, c.p.working)
		}
		c.TextMid(tx, cy, s, t)
		tx += tw + 16
	}
	rowEnd(x+w-12, cy, 8, c.dot(c.p.working, 8), c.txt("go test", ui(13, typeset.Normal, c.p.working)))
	c.Fill(Rect{x, top + 1 + 27, w, 1}, c.p.line)

	termTop := top + 1 + 28
	c.Fill(Rect{x, termTop, w, lowerH - 29}, c.p.termBg)
	tm := TextStyle{Face: Mono, Size: 12, Color: c.p.termFg}
	lines := [][]Token{
		{{"● Edit internal/pricing/round.go (+7 −1)", c.p.fg4, false}},
		{{"● Bash", c.p.fg4, false}, {" ", c.p.termFg, false}, {"go test ./internal/pricing/ -run TestRoundHalfEven -v", c.p.accent, false}},
		{{"  ", c.p.termFg, false}, {"=== RUN   TestRoundHalfEven/2.5→2", c.p.green, false}},
		{{"  ", c.p.termFg, false}, {"=== RUN   TestRoundHalfEven/3.5→4", c.p.green, false}},
		{{"  ", c.p.termFg, false}, {"--- FAIL: TestRoundHalfEven/-2.5→-2 (0.00s)", c.p.red, false}},
		{{"      round_test.go:31: got -3, want -2", c.p.termFg, false}},
		{{"⠼ thinking · 14.2k ctx · 0:41", c.p.working, false}},
	}
	y := termTop + 8
	for _, l := range lines {
		lx := x + 10
		base := c.Baseline(y+9, tm)
		for _, tk := range l {
			t := tm
			t.Color = tk.Color
			lx += c.Text(lx, base, tk.Text, t)
		}
		y += 18
	}
}

func (c *Canvas) rightRail(x, top, bot float32) {
	c.Fill(Rect{x, top, rightW, bot - top}, c.p.bg1)
	c.Fill(Rect{x, top, 1, bot - top}, c.p.line)
	x++
	w := float32(rightW - 1)
	c.Fill(Rect{x, top + 29, w, 1}, c.p.line)
	cy := top + 15
	row(x+10, cy, 8, c.txt("REQUESTS", c.capStyle()), gap(14), c.chip("1 NEEDS YOU", c.p.red), c.chip("1 WORKING", c.p.working))
	rowEnd(x+w-10, cy, 0, c.txt("3 resolved", mono(13, typeset.Normal, c.p.fg3)))

	reqs := []struct {
		dot     color.NRGBA
		q, meta string
		faded   bool
	}{
		{c.p.working, "Negative values round the wrong way", "round.go:46 · claude working · 1m", false},
		{c.p.red, "Move currency precision table to config?", "PLAN.md:22 · awaiting you · 4m", false},
		{c.p.green, "Use banker's rounding for JPY", "resolved · 12m", true},
		{c.p.green, "Rename Mode → RoundingMode", "resolved · 19m", true},
	}
	y := top + 30
	for _, r := range reqs {
		var pop func()
		if r.faded {
			pop = c.Opacity(.55)
		}
		qh := float32(12.5 * 1.45)
		row(x+12, y+10+5+3.5, 0, c.dot(r.dot, 8))
		c.TextMid(x+12+7+10, y+10+qh/2, r.q, ui(12.5, typeset.Normal, c.p.fg))
		c.TextMid(x+12+7+10, y+10+qh+3+7.6, r.meta, mono(10.5, typeset.Normal, c.p.fg3))
		if pop != nil {
			pop()
		}
		h := 10 + qh + 3 + 15.2 + 10
		c.Fill(Rect{x, y + h, w, 1}, c.p.line)
		y += h + 1
	}

	y += 6
	c.Fill(Rect{x, y, w, 1}, c.p.line)
	c.Fill(Rect{x, y + 30, w, 1}, c.p.line)
	row(x+10, y+15.5, 0, c.txt("TASKS", c.capStyle()))
	rowEnd(x+w-10, y+15.5, 0, c.txt("2/5", mono(10, typeset.Normal, c.p.fg3)))
	y += 31 + 8
	tasks := []struct {
		mark string
		mc   color.NRGBA
		s    string
		tc   color.NRGBA
	}{{"✓", c.p.green, "Add RoundingMode type", c.p.fg2}, {"✓", c.p.green, "Half-even for minor units", c.p.fg2}, {"◐", c.p.working, "Negative tie handling", c.p.fg}, {"○", c.p.fg3, "Currency precision table", c.p.fg2}, {"○", c.p.fg3, "Commit + PR draft", c.p.fg2}}
	for _, t := range tasks {
		mt := ui(12, typeset.Normal, t.mc)
		mw := c.TextMid(x+12, y+12, t.mark, mt)
		sp := c.Measure("  ", ui(12, typeset.Normal, c.p.fg2))
		c.TextMid(x+12+mw+sp, y+12, t.s, ui(12, typeset.Normal, t.tc))
		y += 24
	}

	// Session footer.
	ft := bot - (10 + 13 + 8 + 15 + 8 + 15 + 10)
	c.Fill(Rect{x, ft, w, 1}, c.p.line)
	y = ft + 1 + 10
	row(x+12, y+6, 0, c.txt("SESSION", c.capStyle()))
	rowEnd(x+w-12, y+6, 0, c.txt("claude opus · high", mono(10, typeset.Normal, c.p.fg3)))
	indicator := mono(10.5, typeset.Medium, c.p.fg2)
	b := mono(10.5, typeset.Medium, c.p.fg)
	y += 13 + 8
	row(x+12, y+7.5, 6, c.txt("CTX", indicator), c.bar(.22, c.p.accent), c.txt("44k", b), gap(12), c.txt("CACHE", indicator), c.txt("91%", b), gap(12), c.txt("$", indicator), c.txt("1.84", b))
	y += 15 + 8
	row(x+12, y+7.5, 6, c.txt("REVIEWED", indicator), c.bar(.6, c.p.green), c.txt("12/20", b), gap(12), c.txt("TESTS", indicator), c.txt("1 fail", mono(10.5, typeset.Medium, c.p.red)))
}

func (c *Canvas) statusline(top float32) {
	c.Fill(Rect{0, top, IDEWidth, statusH}, c.p.bg1)
	c.Fill(Rect{0, top, IDEWidth, 1}, c.p.line)
	cy := top + 1 + 12.5
	mt := TextStyle{Face: Mono, Weight: typeset.Bold, Size: 10.5, Color: c.p.ink, Spacing: .12}
	mw := c.Measure("NORMAL", mt) + 24
	c.Fill(Rect{0, top + 1, mw, statusH - 1}, c.p.accent)
	c.TextMid(12, cy, "NORMAL", mt)
	st := mono(10.5, typeset.Medium, c.p.fg2)
	dim := mono(10.5, typeset.Medium, c.p.fg3)
	sep := item{1, func(x, cy float32) { c.Fill(Rect{x, cy - 7, 1, 14}, c.p.line2) }}
	row(mw+16, cy, 16,
		c.txt("rethink/rounding", st), c.txt("↑2 ●3", dim), sep,
		c.txt("✕ 0", mono(10.5, typeset.Medium, c.p.red)), c.txt("⚠ 1", mono(10.5, typeset.Medium, c.p.warn)), sep,
		c.txt(`"a`, dim), c.txt("q recording @q", dim))
	spark := item{7*2 + 6, func(x, cy float32) {
		for i, h := range []float32{4, 7, 5, 9, 11, 6, 8} {
			c.Fill(Rect{x + float32(i)*3, cy + 6 - h, 2, h}, alpha(c.p.accent, .75))
		}
	}}
	b := mono(10.5, typeset.Medium, c.p.fg)
	rowEnd(IDEWidth-12, cy, 16,
		item{width([]item{c.txt("WKR mbp", st), spark, c.txt("cpu", st), c.txt("34%", b), c.txt("mem", st), c.txt("11.2G", b)}, 6), func(x, cy float32) {
			row(x, cy, 6, c.txt("WKR mbp", st), spark, c.txt("cpu", st), c.txt("34%", b), c.txt("mem", st), c.txt("11.2G", b))
		}},
		item{width([]item{c.txt("devbox", st), c.dot(c.p.green, 5), c.txt("8%", b)}, 6), func(x, cy float32) {
			row(x, cy, 6, c.txt("devbox", st), c.dot(c.p.green, 5), c.txt("8%", b))
		}},
		sep, c.txt("Go · utf-8 · LF", st), c.txt("46:13", st), c.txt("≡ 48%", dim))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
