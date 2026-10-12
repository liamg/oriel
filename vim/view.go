package vim

import (
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// Pos is a line and byte column, both 0-based. Col may equal the line's length: vim's "NUL" position.
type Pos struct {
	Line, Col int
}

// view reads one snapshot line by line, as vim's motions do, caching the lines it has read.
type view struct {
	s     text.Snapshot
	cache map[int][]byte
	folds *foldState // the window's folds, for motions that treat a closed fold as one line
}

// folded is the closed fold holding line.
func (v *view) folded(line int) (int, int, bool) {
	if v.folds == nil {
		return line, line, false
	}
	return v.folds.closedAt(line)
}

func newView(s text.Snapshot) *view { return &view{s: s, cache: map[int][]byte{}} }

func (v *view) lines() int { return v.s.Lines() }

func (v *view) line(n int) []byte {
	if l, ok := v.cache[n]; ok {
		return l
	}
	l := v.s.Line(n)
	v.cache[n] = l
	return l
}

func (v *view) lineLen(n int) int { return len(v.line(n)) }

func (v *view) offset(p Pos) int { return v.s.LineStart(p.Line) + p.Col }

func (v *view) pos(off int) Pos {
	p := v.s.PosAt(off)
	return Pos{p.Line, p.Col}
}

// char returns the rune at p and its size, or 0, 0 on the NUL past the end of the line.
// char returns the character at p and its size, its composing characters included,
// or 0, 0 on the NUL past the end of the line.
func (v *view) char(p Pos) (rune, int) {
	l := v.line(p.Line)
	if p.Col >= len(l) {
		return 0, 0
	}
	r, n := utf8.DecodeRune(l[p.Col:])
	return r, n + composingAfter(l[p.Col+n:])
}

// composing reports whether r is a combining character, which belongs to the character before it.
func composing(r rune) bool {
	return r >= 0x300 && unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
}

// composingAfter is the size of the composing characters at the start of b.
func composingAfter(b []byte) int {
	n := 0
	for n < len(b) {
		r, size := utf8.DecodeRune(b[n:])
		if !composing(r) {
			break
		}
		n += size
	}
	return n
}

func (v *view) gchar(p Pos) rune {
	r, _ := v.char(p)
	return r
}

// prevCol returns the start of the character before col.
func (v *view) prevCol(line, col int) int {
	if col <= 0 {
		return 0
	}
	l := v.line(line)
	for col > 0 {
		r, n := utf8.DecodeLastRune(l[:col])
		col -= n
		if !composing(r) {
			break
		}
	}
	return col
}

// lastCol is the start of the last character on the line, or 0 when it's empty.
func (v *view) lastCol(line int) int { return v.prevCol(line, v.lineLen(line)) }

// inc moves p one character forward, as vim's inc(): 0 within the line, 2 onto the line's NUL,
// 1 onto the next line, -1 at the end of the buffer.
func (v *view) inc(p *Pos) int {
	l := v.line(p.Line)
	if p.Col < len(l) {
		_, n := v.char(*p)
		p.Col += n
		if p.Col < len(l) {
			return 0
		}
		return 2
	}
	if p.Line < v.lines()-1 {
		p.Line++
		p.Col = 0
		return 1
	}
	return -1
}

// dec moves p one character back, as vim's dec(): 0 within the line, 1 onto the previous line's NUL,
// -1 at the start of the buffer.
func (v *view) dec(p *Pos) int {
	if p.Col > 0 {
		p.Col = v.prevCol(p.Line, min(p.Col, v.lineLen(p.Line)))
		return 0
	}
	if p.Line > 0 {
		p.Line--
		p.Col = v.lineLen(p.Line)
		return 1
	}
	return -1
}

// incl is inc that skips over the NUL at the end of a non-empty line.
func (v *view) incl(p *Pos) int {
	r := v.inc(p)
	if r >= 1 && p.Col > 0 {
		r = v.inc(p)
	}
	return r
}

// decl is dec that skips over the NUL at the end of a non-empty line.
func (v *view) decl(p *Pos) int {
	r := v.dec(p)
	if r == 1 && p.Col > 0 {
		r = v.dec(p)
	}
	return r
}

func isWhite(r rune) bool { return r == ' ' || r == '\t' }

// firstNonBlank is the column after the leading white space: vim's beginline(BL_WHITE).
func (v *view) firstNonBlank(line int) int { return v.indentEnd(line) }

// indentEnd is the column after the leading white space.
func (v *view) indentEnd(line int) int {
	l := v.line(line)
	i := 0
	for i < len(l) && (l[i] == ' ' || l[i] == '\t') {
		i++
	}
	return i
}

func (v *view) lineWhite(line int) bool { return v.indentEnd(line) == v.lineLen(line) }

// inindent reports whether p.Col+extra is at or before the first non-blank.
func (v *view) inindent(p Pos, extra int) bool { return v.indentEnd(p.Line) >= p.Col+extra }

const tabstop = 8

// charWidth is how many screen cells r takes at virtual column vcol.
func charWidth(r rune, vcol int) int {
	switch {
	case r == '\t':
		return tabstop - vcol%tabstop
	case composing(r):
		return 0
	case r < 0x20 || r == 0x7f:
		return 2 // ^X
	case r >= 0x80 && r < 0xa0:
		return 4 // <80>
	case wide(r):
		return 2
	}
	return 1
}

func wide(r rune) bool {
	switch {
	case r < 0x1100:
		return false
	case r <= 0x115f, r >= 0x2e80 && r <= 0xa4cf && r != 0x303f, r >= 0xac00 && r <= 0xd7a3,
		r >= 0xf900 && r <= 0xfaff, r >= 0xfe30 && r <= 0xfe4f, r >= 0xff00 && r <= 0xff60,
		r >= 0xffe0 && r <= 0xffe6, r >= 0x1f300 && r <= 0x1f64f, r >= 0x1f900 && r <= 0x1f9ff,
		r >= 0x20000 && r <= 0x3fffd:
		return true
	}
	return false
}

// vcol is the virtual (screen) column where the character at col starts.
func (v *view) vcol(line, col int) int {
	l := v.line(line)
	vc := 0
	for i := 0; i < col && i < len(l); {
		r, n := utf8.DecodeRune(l[i:])
		vc += charWidth(r, vc)
		i += n
	}
	return vc
}

// colAt is the column of the character that covers virtual column want, or the last character
// when the line is shorter. With past, the NUL after the last character is allowed.
func (v *view) colAt(line, want int, past bool) int {
	l := v.line(line)
	vc, i := 0, 0
	for i < len(l) {
		r, n := utf8.DecodeRune(l[i:])
		w := charWidth(r, vc)
		if vc+w > want {
			return i
		}
		vc += w
		if i+n == len(l) && !past {
			return i
		}
		i += n
	}
	return i
}

// Word motions (w, b, e, iw…) split text where the character class changes, as vim's cls() does.
const (
	classBlank = 0 // space, tab, NUL and other blanks
	classPunct = 1 // punctuation and symbols
	classWord  = 2 // letters, digits and _ ('iskeyword')
	classEmoji = 3
	// Scripts with no spaces between words get a class each, so that a run of them is one word
	// and a change of script is a word boundary. The value is the script's first code point.
	classSuperscript = 0x2070
	classSubscript   = 0x2080
	classBraille     = 0x2800
	classHiragana    = 0x3040
	classKatakana    = 0x30a0
	classCJK         = 0x4e00
	classHangul      = 0xac00
)

// charClass is r's class; for WORD motions (W, B, E, iW) every non-blank is one class.
func charClass(r rune, bigword bool) int {
	c := utfClass(r)
	if c != classBlank && bigword {
		return classPunct
	}
	return c
}

func utfClass(r rune) int {
	if r < 0x100 {
		switch {
		case r == ' ' || r == '\t' || r == 0 || r == 0xa0:
			return classBlank
		case isKeyword(r):
			return classWord
		}
		return classPunct
	}
	for _, c := range classes {
		if r >= c.lo && r <= c.hi {
			return c.class
		}
	}
	if unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return classPunct
	}
	return classWord
}

// isKeyword follows nvim's default 'iskeyword': "@,48-57,_,192-255".
func isKeyword(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r >= 192 && r <= 255
}

// classes are the Unicode blocks whose class isn't what unicode.IsPunct and IsSymbol would say,
// after the table in vim's mbyte.c (utf_class).
var classes = []struct {
	lo, hi rune
	class  int
}{
	{0x1680, 0x1680, classBlank},       // Ogham space mark
	{0x2000, 0x200b, classBlank},       // en quad … zero-width space
	{0x200c, 0x2027, classPunct},       // general punctuation
	{0x2028, 0x2029, classBlank},       // line and paragraph separators
	{0x202a, 0x202e, classPunct},       // bidi controls
	{0x202f, 0x202f, classBlank},       // narrow no-break space
	{0x2030, 0x205e, classPunct},       // general punctuation
	{0x205f, 0x205f, classBlank},       // medium mathematical space
	{0x2060, 0x206f, classPunct},       // invisible operators
	{0x2070, 0x207f, classSuperscript}, // superscripts
	{0x2080, 0x2094, classSubscript},   // subscripts
	{0x20a0, 0x27ff, classPunct},       // currency, arrows, maths and other symbols
	{0x2800, 0x28ff, classBraille},     // braille
	{0x2900, 0x2998, classPunct},       // arrows, brackets
	{0x2e00, 0x2e7f, classPunct},       // supplemental punctuation
	{0x3000, 0x3000, classBlank},       // ideographic space
	{0x3001, 0x3020, classPunct},       // CJK punctuation
	{0x3030, 0x3030, classPunct},       // wavy dash
	{0x303d, 0x303d, classPunct},       // part alternation mark
	{0x3040, 0x309f, classHiragana},    // hiragana
	{0x30a0, 0x30ff, classKatakana},    // katakana
	{0x3300, 0x9fff, classCJK},         // CJK ideographs
	{0xac00, 0xd7a3, classHangul},      // Hangul syllables
	{0xf900, 0xfaff, classCJK},         // CJK compatibility ideographs
	{0xfe30, 0xfe6b, classPunct},       // CJK and small form punctuation
	{0xff00, 0xff0f, classPunct},       // fullwidth ASCII punctuation
	{0xff1a, 0xff20, classPunct},       // fullwidth ASCII punctuation
	{0xff3b, 0xff40, classPunct},       // fullwidth ASCII punctuation
	{0xff5b, 0xff65, classPunct},       // fullwidth ASCII punctuation
	{0x1f000, 0x1faff, classEmoji},     // emoji and pictographs
	{0x20000, 0x2fa1f, classCJK},       // CJK ideographs, extension B onwards
}
