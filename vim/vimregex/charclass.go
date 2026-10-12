package vimregex

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type runeRange struct{ lo, hi rune }

type classRange struct {
	lo, hi rune
	class  int
}

// charSet is a [] collection.
type charSet struct {
	ranges  []runeRange // characters and ranges; case is ignored for these with ignorecase
	classes []int       // [:name:] expressions; case always matters
	nl      bool        // [\n]
}

func (s *charSet) add(r rune)           { s.ranges = append(s.ranges, runeRange{r, r}) }
func (s *charSet) addRange(lo, hi rune) { s.ranges = append(s.ranges, runeRange{lo, hi}) }

func (s *charSet) hasExact(r rune) bool {
	for _, rg := range s.ranges {
		if r >= rg.lo && r <= rg.hi {
			return true
		}
	}
	return false
}

func (s *charSet) has(r rune, m *matcher) bool {
	if s.hasExact(r) {
		return true
	}
	if m.ic {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if s.hasExact(f) {
				return true
			}
		}
	}
	for _, c := range s.classes {
		if m.inClassExpr(c, r) {
			return true
		}
	}
	return false
}

var classNames = []string{"alnum:]", "alpha:]", "blank:]", "cntrl:]", "digit:]", "graph:]", "lower:]", "print:]",
	"punct:]", "space:]", "upper:]", "xdigit:]", "tab:]", "return:]", "backspace:]", "escape:]", "ident:]",
	"keyword:]", "fname:]"}

const (
	clsAlnum = iota
	clsAlpha
	clsBlank
	clsCntrl
	clsDigit
	clsGraph
	clsLower
	clsPrint
	clsPunct
	clsSpace
	clsUpper
	clsXdigit
	clsTab
	clsReturn
	clsBackspace
	clsEscape
	clsIdent
	clsKeyword
	clsFname
)

// charClassAt reads "[:name:]" at i, returning the class and the index after it.
func charClassAt(s string, i int) (int, int) {
	if i+1 < len(s) && s[i+1] == ':' {
		for c, name := range classNames {
			if strings.HasPrefix(s[i+2:], name) {
				return c, i + 2 + len(name)
			}
		}
	}
	return -1, i
}

// bracketedAt reads "[=x=]" (kind '=') or "[.x.]" (kind '.') at i.
func bracketedAt(s string, i int, kind byte) (rune, int) {
	if i+2 < len(s) && s[i+1] == kind {
		r, l := decodeAt(s, i+2)
		if i+l+3 < len(s) && s[i+l+2] == kind && s[i+l+3] == ']' {
			return r, i + l + 4
		}
	}
	return 0, i
}

// skipAnyof returns the index of the ] that ends a collection starting at i, or len(s) if there is none.
func skipAnyof(s string, i int) int {
	at := func(j int) byte {
		if j < len(s) {
			return s[j]
		}
		return 0
	}
	if at(i) == '^' {
		i++
	}
	if at(i) == ']' || at(i) == '-' {
		i++
	}
	for i < len(s) && s[i] != ']' {
		_, l := decodeAt(s, i)
		switch {
		case l > 1:
			i += l
		case s[i] == '-':
			i++
			if i < len(s) && s[i] != ']' {
				_, l := decodeAt(s, i)
				i += l
			}
		case s[i] == '\\' && i+1 < len(s) && (strings.IndexByte(`]^-n\`, s[i+1]) >= 0 || strings.IndexByte("nrtebdoxuU", s[i+1]) >= 0):
			i += 2
		case s[i] == '[':
			j := i
			if _, k := charClassAt(s, j); k != j {
				i = k
			} else if _, k := bracketedAt(s, j, '='); k != j {
				i = k
			} else if _, k := bracketedAt(s, j, '.'); k != j {
				i = k
			} else {
				i++
			}
		default:
			i++
		}
	}
	return i
}

// collGetChar reads \d123, \o40, \x20, € or \U... at p.pos (on the letter); when no number follows
// it is a backslash and the letter stays to be read.
func (p *parser) collGetChar() int {
	c := p.src[p.pos]
	p.pos++
	var n int64 = -1
	switch c {
	case 'd':
		n = p.getDec()
	case 'o':
		n = p.getOct()
	case 'x':
		n = p.getHex(2)
	case 'u':
		n = p.getHex(4)
	case 'U':
		n = p.getHex(8)
	}
	if n < 0 || n > 0x7fffffff {
		p.pos--
		return '\\'
	}
	return int(n)
}

// collection parses [...] after the [. It returns nil, with no error, when there is no closing ].
func (p *parser) collection(nl bool) *node {
	if end := skipAnyof(p.src, p.pos); end >= len(p.src) {
		return nil
	}
	set := &charSet{}
	n := &node{op: opSet, set: set, nl: nl}
	if p.at(p.pos) == '^' {
		n.neg = true
		p.pos++
	}
	startc := -1
	if c := p.at(p.pos); c == ']' || c == '-' {
		startc = int(c)
		set.add(rune(c))
		p.pos++
	}
	for p.pos < len(p.src) && p.src[p.pos] != ']' {
		switch c := p.src[p.pos]; {
		case c == '-':
			p.pos++
			if p.pos >= len(p.src) || p.src[p.pos] == ']' || startc == -1 || (p.src[p.pos] == '\\' && p.at(p.pos+1) == 'n') {
				set.add('-')
				startc = '-'
				break
			}
			endc := 0
			if p.src[p.pos] == '[' {
				if r, k := bracketedAt(p.src, p.pos, '.'); k != p.pos {
					endc = int(r)
					p.pos = k
				}
			}
			if endc == 0 {
				r, l := decodeAt(p.src, p.pos)
				endc = int(r)
				p.pos += l
			}
			if endc == '\\' && p.pos < len(p.src) {
				endc = p.collGetChar()
			}
			if startc > endc {
				return p.fail("E944: Reverse range in character class")
			}
			set.addRange(nul(rune(startc)), rune(endc))
			startc = -1
		case c == '\\' && p.pos+1 < len(p.src) && (strings.IndexByte(`]^-n\`, p.src[p.pos+1]) >= 0 || strings.IndexByte("nrtebdoxuU", p.src[p.pos+1]) >= 0):
			p.pos++
			switch e := p.src[p.pos]; e {
			case 'n':
				if !n.neg {
					set.nl = true
				}
				p.pos++
				startc = -1
			case 'd', 'o', 'x', 'u', 'U':
				startc = p.collGetChar()
				set.add(nul(rune(startc)))
			default:
				startc = int(backslashTrans(e))
				set.add(rune(startc))
				p.pos++
			}
		case c == '[':
			startc = -1
			if cls, k := charClassAt(p.src, p.pos); k != p.pos {
				set.classes = append(set.classes, cls)
				p.pos = k
			} else if r, k := bracketedAt(p.src, p.pos, '='); k != p.pos {
				addEquivalence(set, r)
				p.pos = k
			} else if r, k := bracketedAt(p.src, p.pos, '.'); k != p.pos {
				set.add(r)
				p.pos = k
			} else {
				startc = '['
				set.add('[')
				p.pos++
			}
		default:
			r, l := decodeAt(p.src, p.pos)
			p.pos += l
			startc = int(r)
			set.add(nul(r))
			for p.pos < len(p.src) {
				cr, cl := decodeAt(p.src, p.pos)
				if !isComposing(cr) {
					break
				}
				startc = -1
				set.add(cr)
				p.pos += cl
			}
		}
	}
	if p.err != nil {
		return nil
	}
	p.skip()
	return n
}

func addEquivalence(set *charSet, r rune) {
	for _, eq := range equivalences {
		if strings.ContainsRune(eq, r) {
			for _, e := range eq {
				set.add(e)
			}
			return
		}
	}
	set.add(r)
}

func inRanges(rs []runeRange, r rune) bool {
	i := sort.Search(len(rs), func(i int) bool { return rs[i].hi >= r })
	return i < len(rs) && rs[i].lo <= r
}

func isComposing(r rune) bool {
	return r >= composing[0].lo && inRanges(composing, r)
}

// utfClass is vim's character class: 0 blank, 1 punctuation, 2 word, 3 emoji, others a script.
func utfClass(r rune) int {
	i := sort.Search(len(charClasses), func(i int) bool { return charClasses[i].hi >= r })
	if i < len(charClasses) && charClasses[i].lo <= r {
		return charClasses[i].class
	}
	return 2
}

func isLowerVim(r rune) bool {
	if r < 0x80 {
		return r >= 'a' && r <= 'z'
	}
	return unicode.ToUpper(r) != r || r == 0xdf
}

func isUpperVim(r rune) bool {
	if r < 0x80 {
		return r >= 'A' && r <= 'Z'
	}
	return unicode.ToLower(r) != r
}

func isASCIIAlpha(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }
func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }
func isHex(r rune) bool        { return isASCIIDigit(r) || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F' }
func isPunct(r rune) bool {
	return r > ' ' && r < 0x7f && !isASCIIAlpha(r) && !isASCIIDigit(r)
}

func (m *matcher) isKeyword(r rune) bool {
	if m.st.IsKeyword != nil {
		return m.st.IsKeyword(r)
	}
	if r >= 0 && r < 0x100 {
		return latin1[r]&1 != 0
	}
	return utfClass(r) >= 2
}

func (m *matcher) isIdent(r rune) bool {
	if m.st.IsIdent != nil {
		return m.st.IsIdent(r)
	}
	return r >= 0 && r < 0x100 && latin1[r]&2 != 0
}

func (m *matcher) isFname(r rune) bool {
	if m.st.IsFname != nil {
		return m.st.IsFname(r)
	}
	return r >= 0x100 || r >= 0 && latin1[r]&4 != 0
}

func (m *matcher) isPrint(r rune) bool {
	if m.st.IsPrint != nil {
		return m.st.IsPrint(r)
	}
	if r >= 0 && r < 0x100 {
		return latin1[r]&8 != 0
	}
	return !inRanges(unprintable, r)
}

// wordClass is the class \< and \> compare: 0 blank, 1 punctuation, 2 keyword, more for other scripts.
func (m *matcher) wordClass(r rune) int {
	if r == ' ' || r == '\t' || r == 0 || r == 0xa0 {
		return 0
	}
	if r < 0x100 {
		if m.isKeyword(r) {
			return 2
		}
		return 1
	}
	return utfClass(r)
}

// inClass matches the \i \k \s ... atoms; ignorecase never applies to them.
func (m *matcher) inClass(c byte, r rune) bool {
	switch c {
	case '.':
		return true
	case 'i':
		return m.isIdent(r)
	case 'I':
		return m.isIdent(r) && !isASCIIDigit(r)
	case 'k':
		return m.isKeyword(r)
	case 'K':
		return m.isKeyword(r) && !isASCIIDigit(r)
	case 'f':
		return m.isFname(r)
	case 'F':
		return m.isFname(r) && !isASCIIDigit(r)
	case 'p':
		return m.isPrint(r)
	case 'P':
		return m.isPrint(r) && !isASCIIDigit(r)
	case 's':
		return r == ' ' || r == '\t'
	case 'S':
		return r != ' ' && r != '\t'
	case 'd':
		return isASCIIDigit(r)
	case 'D':
		return !isASCIIDigit(r)
	case 'x':
		return isHex(r)
	case 'X':
		return !isHex(r)
	case 'o':
		return r >= '0' && r <= '7'
	case 'O':
		return !(r >= '0' && r <= '7')
	case 'w':
		return isASCIIAlpha(r) || isASCIIDigit(r) || r == '_'
	case 'W':
		return !(isASCIIAlpha(r) || isASCIIDigit(r) || r == '_')
	case 'h':
		return isASCIIAlpha(r) || r == '_'
	case 'H':
		return !(isASCIIAlpha(r) || r == '_')
	case 'a':
		return isASCIIAlpha(r)
	case 'A':
		return !isASCIIAlpha(r)
	case 'l':
		return r >= 'a' && r <= 'z'
	case 'L':
		return !(r >= 'a' && r <= 'z')
	case 'u':
		return r >= 'A' && r <= 'Z'
	case 'U':
		return !(r >= 'A' && r <= 'Z')
	}
	return false
}

func (m *matcher) inClassExpr(c int, r rune) bool {
	switch c {
	case clsAlnum:
		return isASCIIAlpha(r) || isASCIIDigit(r)
	case clsAlpha:
		return isASCIIAlpha(r)
	case clsBlank:
		return r == ' ' || r == '\t'
	case clsCntrl:
		return r >= 1 && r < 0x20 || r == 0x7f
	case clsDigit:
		return isASCIIDigit(r)
	case clsGraph:
		return r > ' ' && r < 0x7f
	case clsLower:
		return isLowerVim(r) && r != 170 && r != 186
	case clsPrint:
		return m.isPrint(r)
	case clsPunct:
		return isPunct(r)
	case clsSpace:
		return r >= 9 && r <= 13 || r == ' '
	case clsUpper:
		return isUpperVim(r)
	case clsXdigit:
		return isHex(r)
	case clsTab:
		return r == '\t'
	case clsReturn:
		return r == '\r'
	case clsBackspace:
		return r == '\b'
	case clsEscape:
		return r == 0x1b
	case clsIdent:
		return m.isIdent(r)
	case clsKeyword:
		return m.isKeyword(r)
	case clsFname:
		return m.isFname(r)
	}
	return false
}

func foldEq(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

// decode reads the character at i; an invalid byte is a character of its own.
func decode(b []byte, i int) (rune, int) {
	r, n := utf8.DecodeRune(b[i:])
	if r == utf8.RuneError && n <= 1 {
		return rune(b[i]), 1
	}
	return r, n
}

// clusterEnd is the end of the character at i with its composing characters.
func clusterEnd(b []byte, i int) int {
	_, n := decode(b, i)
	i += n
	for i < len(b) {
		r, n := decode(b, i)
		if !isComposing(r) {
			break
		}
		i += n
	}
	return i
}

func prevRuneStart(b []byte, i int) int {
	_, n := utf8.DecodeLastRune(b[:i])
	if n < 1 {
		n = 1
	}
	return i - n
}

// clusterStart is the start of the character that ends at i, with its composing characters.
func clusterStart(b []byte, i int) int {
	j := prevRuneStart(b, i)
	if r, _ := decode(b, j); !isComposing(r) {
		return j
	}
	for j > 0 {
		k := prevRuneStart(b, j)
		j = k
		if r, _ := decode(b, k); !isComposing(r) {
			return k
		}
	}
	return j
}
