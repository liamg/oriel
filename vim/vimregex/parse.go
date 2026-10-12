package vimregex

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// The lexer and parser follow regexp.c and regexp_bt.c: peek, skip, get and unget work on tokens, where a
// character that is special at the current magic level carries magicBit.

const (
	magicNone = 1 // \V
	magicOff  = 2 // \M
	magicOn   = 3 // \m
	magicAll  = 4 // \v

	magicBit = 1 << 30
	tokEOF   = -2
	infinite = math.MaxInt32
)

func mg(c int) int       { return c | magicBit }
func isMagic(c int) bool { return c >= 0 && c&magicBit != 0 }
func noMagic(c int) int {
	if c < 0 {
		return c
	}
	return c &^ magicBit
}
func toggleMagic(c int) int {
	if c < 0 {
		return c
	}
	return c ^ magicBit
}

// meta holds the characters that may be magic after a backslash.
const meta = "%&()*+.123456789<=>?@ACDFHIKLMOPSUVWXZ[_acdfhiklmnopsuvwxz{|~"

const classChars = ".iIkKfFpPsSdDxXoOwWhHaAlLuU"

const (
	regNoParen = iota
	regParen
	regNParen
)

type op uint8

const (
	opEmpty    op = iota
	opChar        // r, comb
	opCombOnly    // a composing character on its own
	opAny
	opClass // cls
	opSet   // set, neg
	opNL    // \n
	opBOL
	opEOL
	opBOW
	opEOW
	opBOF
	opEOF
	opCursor
	opVisual
	opMark // mark, cmp
	opLnum // num, cmp, cur
	opCol  // num, cmp, cur
	opVcol // num, cmp, cur
	opSkipComb
	opZS
	opZE
	opBackref // num
	opCapture // num, subs[0]
	opConcat
	opAlt
	opRepeat // subs[0], min, max, greedy
	opAhead
	opNotAhead
	opBehind    // num is the byte limit, 0 for none
	opNotBehind // num is the byte limit, 0 for none
	opAtomic
)

type node struct {
	op      op
	r       rune
	comb    []rune // composing characters after r
	anyComb bool   // followed by \%C
	cls     byte
	nl      bool // also matches an end-of-line
	set     *charSet
	neg     bool
	num     int
	min     int
	max     int
	greedy  bool
	cmp     byte
	cur     bool
	subs    []*node
}

// unit reports whether n always consumes exactly one character or one end-of-line.
func (n *node) unit() bool {
	switch n.op {
	case opChar, opCombOnly, opAny, opClass, opSet, opNL:
		return true
	}
	return false
}

type parser struct {
	src         string
	pos         int
	magic       int
	cur, next   int
	prev, prev2 int
	prevLen     int
	atStart     bool
	prevAtStart bool
	afterSlash  int
	npar        int
	hadEnd      [10]bool
	icase       bool
	noicase     bool
	icombine    bool
	lastSub     string
	err         error
}

func newParser(src string, magic int, lastSub string) *parser {
	return &parser{src: src, magic: magic, cur: -1, next: -1, prev: -1, prev2: -1, atStart: true, npar: 1, lastSub: lastSub}
}

func (p *parser) parse() *node {
	n := p.reg(regNoParen)
	if p.err != nil {
		return nil
	}
	markAnyComb(n)
	return n
}

func (p *parser) fail(format string, args ...any) *node {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
	return nil
}

// bs is the backslash vim's messages put before an item, empty when the item needs none.
func bs(noBackslash bool) string {
	if noBackslash {
		return ""
	}
	return `\`
}

func (p *parser) at(i int) byte {
	if i < len(p.src) {
		return p.src[i]
	}
	return 0
}

func decodeAt(s string, i int) (rune, int) {
	r, n := utf8.DecodeRuneInString(s[i:])
	if r == utf8.RuneError && n <= 1 {
		return rune(s[i]), 1
	}
	return r, n
}

func (p *parser) peek() int {
	if p.cur != -1 {
		return p.cur
	}
	if p.pos >= len(p.src) {
		p.cur = tokEOF
		return p.cur
	}
	c := p.src[p.pos]
	switch c {
	case '.', '[', '~':
		p.cur = int(c)
		if p.magic >= magicOn {
			p.cur = mg(int(c))
		}
	case '(', ')', '{', '%', '+', '=', '?', '@', '!', '&', '|', '<', '>', '#', '"', '\'', ',', '-', ':', ';', '`', '/':
		p.cur = int(c)
		if p.magic == magicAll {
			p.cur = mg(int(c))
		}
	case '*':
		p.cur = '*'
		if p.magic >= magicOn && !p.atStart && !(p.prevAtStart && p.prev == mg('^')) &&
			(p.afterSlash > 0 || (p.prev != mg('(') && p.prev != mg('&') && p.prev != mg('|'))) {
			p.cur = mg('*')
		}
	case '^':
		p.cur = '^'
		if p.magic >= magicOff && (p.atStart || p.magic == magicAll || p.prev == mg('(') || p.prev == mg('|') ||
			p.prev == mg('&') || p.prev == mg('n') || (noMagic(p.prev) == '(' && p.prev2 == mg('%'))) {
			p.cur = mg('^')
			p.atStart = true
			p.prevAtStart = false
		}
	case '$':
		p.cur = '$'
		if p.magic >= magicOff {
			i := p.pos + 1
			all := p.magic == magicAll
			for p.at(i) == '\\' && strings.IndexByte("cCmMvVZ", p.at(i+1)) >= 0 && p.at(i+1) != 0 {
				switch p.at(i + 1) {
				case 'v':
					all = true
				case 'm', 'M', 'V':
					all = false
				}
				i += 2
			}
			c0, c1 := p.at(i), p.at(i+1)
			if i >= len(p.src) || (c0 == '\\' && (c1 == '|' || c1 == '&' || c1 == ')' || c1 == 'n')) ||
				(all && (c0 == '|' || c0 == '&' || c0 == ')')) || p.magic == magicAll {
				p.cur = mg('$')
			}
		}
	case '\\':
		if p.pos+1 >= len(p.src) {
			p.cur = '\\'
			break
		}
		c2 := p.src[p.pos+1]
		switch {
		case c2 <= '~' && strings.IndexByte(meta, c2) >= 0:
			p.cur = -1
			p.prevAtStart = p.atStart
			p.atStart = false
			p.pos++
			p.afterSlash++
			p.peek()
			p.pos--
			p.afterSlash--
			p.cur = toggleMagic(p.cur)
		case strings.IndexByte("nrtebdoxuU", c2) >= 0:
			p.cur = int(backslashTrans(c2))
		case p.magic == magicNone && (c2 == '$' || c2 == '^'):
			p.cur = mg(int(c2))
		default:
			r, _ := decodeAt(p.src, p.pos+1)
			p.cur = int(r)
		}
	default:
		r, _ := decodeAt(p.src, p.pos)
		p.cur = int(r)
	}
	return p.cur
}

func backslashTrans(c byte) rune {
	switch c {
	case 'r':
		return '\r'
	case 't':
		return '\t'
	case 'e':
		return 0x1b
	case 'b':
		return '\b'
	}
	return rune(c)
}

func (p *parser) skip() {
	p.prevLen = 0
	if p.at(p.pos) == '\\' && p.pos < len(p.src) {
		p.prevLen = 1
	}
	if p.pos+p.prevLen < len(p.src) {
		_, n := decodeAt(p.src, p.pos+p.prevLen)
		p.prevLen += n
	}
	p.pos += p.prevLen
	p.prevAtStart = p.atStart
	p.atStart = false
	p.prev2 = p.prev
	p.prev = p.cur
	p.cur = p.next
	p.next = -1
}

func (p *parser) skipKeepStart() {
	as, pr, pr2 := p.prevAtStart, p.prev, p.prev2
	p.skip()
	p.atStart, p.prev, p.prev2 = as, pr, pr2
}

func (p *parser) get() int {
	c := p.peek()
	p.skip()
	return c
}

func (p *parser) unget() {
	p.next = p.cur
	p.cur = p.prev
	p.prev = p.prev2
	p.atStart = p.prevAtStart
	p.prevAtStart = false
	p.pos -= p.prevLen
}

func multiType(c int) int {
	switch c {
	case mg('@'), mg('='), mg('?'):
		return 1
	case mg('*'), mg('+'), mg('{'):
		return 2
	}
	return 0
}

func (p *parser) reg(paren int) *node {
	capIdx := -1
	if paren == regParen {
		if p.npar >= 10 {
			return p.fail("E872: (NFA regexp) Too many '('")
		}
		capIdx = p.npar
		p.npar++
	}
	var alts []*node
	for {
		br := p.branch()
		if p.err != nil {
			return nil
		}
		alts = append(alts, br)
		if p.peek() != mg('|') {
			break
		}
		p.skip()
	}
	if paren != regNoParen {
		if p.get() != mg(')') {
			if paren == regNParen {
				return p.fail("E53: Unmatched %s%%(", bs(p.magic == magicAll))
			}
			return p.fail("E54: Unmatched %s(", bs(p.magic == magicAll))
		}
	} else if p.peek() != tokEOF {
		if p.cur == mg(')') {
			return p.fail("E55: Unmatched %s)", bs(p.magic == magicAll))
		}
		return p.fail("E488: Trailing characters")
	}
	n := alts[0]
	if len(alts) > 1 {
		n = &node{op: opAlt, subs: alts}
	}
	if capIdx >= 0 {
		p.hadEnd[capIdx] = true
		n = &node{op: opCapture, num: capIdx, subs: []*node{n}}
	}
	return n
}

// branch is concats joined by \&: every concat but the last must match at the same position.
func (p *parser) branch() *node {
	var parts []*node
	for {
		c := p.concat()
		if p.err != nil {
			return nil
		}
		if p.peek() != mg('&') {
			parts = append(parts, c)
			break
		}
		p.skip()
		parts = append(parts, &node{op: opAhead, subs: []*node{c}})
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return &node{op: opConcat, subs: parts}
}

func (p *parser) concat() *node {
	var items []*node
	for {
		switch p.peek() {
		case tokEOF, mg('|'), mg('&'), mg(')'):
			switch len(items) {
			case 0:
				return &node{op: opEmpty}
			case 1:
				return items[0]
			}
			return &node{op: opConcat, subs: items}
		case mg('Z'):
			p.icombine = true
			p.skipKeepStart()
		case mg('c'):
			p.icase = true
			p.skipKeepStart()
		case mg('C'):
			p.noicase = true
			p.skipKeepStart()
		case mg('v'), mg('m'), mg('M'), mg('V'):
			switch p.cur {
			case mg('v'):
				p.magic = magicAll
			case mg('m'):
				p.magic = magicOn
			case mg('M'):
				p.magic = magicOff
			default:
				p.magic = magicNone
			}
			p.skipKeepStart()
			p.cur = -1
		default:
			n := p.piece()
			if n == nil {
				return nil
			}
			items = append(items, n)
		}
	}
}

func (p *parser) piece() *node {
	atom := p.atom()
	if atom == nil {
		return nil
	}
	c := p.peek()
	if multiType(c) == 0 {
		return atom
	}
	p.skip()
	var n *node
	switch c {
	case mg('*'):
		n = &node{op: opRepeat, subs: []*node{atom}, min: 0, max: infinite, greedy: true}
	case mg('+'):
		n = &node{op: opRepeat, subs: []*node{atom}, min: 1, max: infinite, greedy: true}
	case mg('='), mg('?'):
		n = &node{op: opRepeat, subs: []*node{atom}, min: 0, max: 1, greedy: true}
	case mg('@'):
		limit := p.getDec()
		var o op
		opc := noMagic(p.get())
		switch opc {
		case '=':
			o = opAhead
		case '!':
			o = opNotAhead
		case '>':
			o = opAtomic
		case '<':
			opc = noMagic(p.get())
			switch opc {
			case '=':
				o = opBehind
			case '!':
				o = opNotBehind
			}
		}
		if o == 0 {
			return p.unknownOp("E869", `\@`, opc)
		}
		n = &node{op: o, subs: []*node{atom}, num: max(int(limit), 0)}
	case mg('{'):
		lo, hi, lazy, ok := p.readLimits()
		if !ok {
			return nil
		}
		n = &node{op: opRepeat, subs: []*node{atom}, min: lo, max: hi, greedy: !lazy}
	}
	if c2 := p.peek(); multiType(c2) != 0 {
		return p.fail("E871: (NFA regexp) Can't have a multi follow a multi")
	}
	return n
}

// readLimits reads the inside of \{n,m}; a leading - makes it lazy.
func (p *parser) readLimits() (lo, hi int, lazy, ok bool) {
	rev := false
	if p.at(p.pos) == '-' {
		p.pos++
		rev = true
	}
	first := p.pos
	minv := p.getDigits()
	var maxv int
	if p.at(p.pos) == ',' {
		p.pos++
		if isDigit(p.at(p.pos)) {
			maxv = p.getDigits()
		} else {
			maxv = infinite
		}
	} else if isDigit(p.at(first)) {
		maxv = minv
	} else {
		maxv = infinite
	}
	if p.at(p.pos) == '\\' {
		p.pos++
	}
	if p.at(p.pos) != '}' || p.pos >= len(p.src) {
		p.fail("E554: Syntax error in %s{...}", bs(p.magic == magicAll))
		return 0, 0, false, false
	}
	if (!rev && minv > maxv) || (rev && minv < maxv) {
		minv, maxv = maxv, minv
	}
	p.skip()
	if minv > maxv {
		return maxv, minv, true, true
	}
	return minv, maxv, false, true
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func (p *parser) getDigits() int {
	n := 0
	for isDigit(p.at(p.pos)) && p.pos < len(p.src) {
		if n < infinite/10 {
			n = n*10 + int(p.src[p.pos]-'0')
		}
		p.pos++
	}
	return n
}

func (p *parser) getDec() int64 {
	var n int64
	i := 0
	for ; p.pos < len(p.src) && isDigit(p.src[p.pos]); i++ {
		if n <= math.MaxInt32 {
			n = n*10 + int64(p.src[p.pos]-'0')
		}
		p.pos++
		p.cur = -1
	}
	if i == 0 {
		return -1
	}
	return n
}

func (p *parser) getOct() int64 {
	var n int64
	i := 0
	for ; i < 3 && n < 040 && p.pos < len(p.src); i++ {
		c := p.src[p.pos]
		if c < '0' || c > '7' {
			break
		}
		n = n<<3 | int64(c-'0')
		p.pos++
	}
	if i == 0 {
		return -1
	}
	return n
}

func (p *parser) getHex(maxLen int) int64 {
	var n int64
	i := 0
	for ; i < maxLen && p.pos < len(p.src); i++ {
		v := hexVal(p.src[p.pos])
		if v < 0 {
			break
		}
		n = n<<4 | int64(v)
		p.pos++
	}
	if i == 0 {
		return -1
	}
	return n
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// nul maps a pattern character to the text's: vim keeps a NUL as NL, so both match a NUL byte.
func nul(r rune) rune {
	if r == '\n' {
		return 0
	}
	return r
}

func (p *parser) atom() *node {
	savePrevAtStart := p.prevAtStart
	c := p.get()
	switch c {
	case mg('^'):
		return &node{op: opBOL}
	case mg('$'):
		return &node{op: opEOL}
	case mg('<'):
		return &node{op: opBOW}
	case mg('>'):
		return &node{op: opEOW}
	case mg('_'):
		c2 := noMagic(p.get())
		switch {
		case c2 == '^':
			return &node{op: opBOL}
		case c2 == '$':
			return &node{op: opEOL}
		case c2 == '[':
			if n := p.collection(true); n != nil || p.err != nil {
				return n
			}
			return p.literal('[')
		case c2 >= 0 && c2 < 128 && strings.IndexByte(classChars, byte(c2)) >= 0:
			return classNode(byte(c2), true)
		}
		if c2 == tokEOF {
			return p.fail("E865: (NFA) Regexp end encountered prematurely")
		}
		return p.fail("E877: (NFA regexp) Invalid character class: %d", c2)
	case mg('n'):
		return &node{op: opNL}
	case mg('('):
		return p.reg(regParen)
	case tokEOF:
		return p.fail("E865: (NFA) Regexp end encountered prematurely")
	case mg('|'), mg('&'), mg(')'), mg('='), mg('?'), mg('+'), mg('@'), mg('{'), mg('*'):
		return p.fail("E866: (NFA regexp) Misplaced %c", rune(noMagic(c)))
	case mg('~'):
		return p.lastSubNode()
	case mg('z'):
		switch noMagic(p.get()) {
		case 's':
			if multiType(p.peek()) == 2 {
				return p.fail(`E888: (NFA regexp) cannot repeat \zs`)
			}
			return &node{op: opZS}
		case 'e':
			if multiType(p.peek()) == 2 {
				return p.fail(`E888: (NFA regexp) cannot repeat \ze`)
			}
			return &node{op: opZE}
		case '(':
			return p.fail(`E66: \z( not allowed here`)
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			return p.fail(`E67: \z1 - \z9 not allowed here`)
		}
		return p.unknownOp("E867", `\z`, p.prev)
	case mg('%'):
		return p.percent(savePrevAtStart)
	case mg('['):
		if n := p.collection(false); n != nil || p.err != nil {
			return n
		}
		return p.literal('[')
	}
	if c >= mg('1') && c <= mg('9') {
		ref := noMagic(c) - '0'
		if !p.seenEndBrace(ref) {
			return nil
		}
		return &node{op: opBackref, num: ref}
	}
	if isMagic(c) && noMagic(c) < 128 && strings.IndexByte(classChars, byte(noMagic(c))) >= 0 {
		if c == mg('.') {
			if pk := p.peek(); pk >= 0 && !isMagic(pk) && isComposing(rune(pk)) {
				p.skip()
				return &node{op: opCombOnly, r: rune(pk)}
			}
		}
		return classNode(byte(noMagic(c)), false)
	}
	r := rune(noMagic(c))
	if r >= 0x80 && !isMagic(c) && p.pos >= p.prevLen && p.src[p.pos-p.prevLen] == '\\' {
		// nvim reads a backslashed multibyte character as itself and then reads it again.
		p.pos -= p.prevLen - 1
		p.cur = -1
		return &node{op: opChar, r: r}
	}
	return p.literal(r)
}

// unknownOp fails with nvim's message for an unknown item; at the end of the pattern the message
// stops short, as nvim's does at the NUL.
func (p *parser) unknownOp(code, item string, c int) *node {
	if c < 0 {
		return p.fail("%s: (NFA) Unknown operator '%s", code, item)
	}
	return p.fail("%s: (NFA) Unknown operator '%s%c'", code, item, rune(noMagic(c)))
}

func classNode(c byte, nl bool) *node {
	if c == '.' {
		return &node{op: opAny, nl: nl}
	}
	return &node{op: opClass, cls: c, nl: nl}
}

// literal is a character with the composing characters that follow it in the pattern.
func (p *parser) literal(r rune) *node {
	r = nul(r)
	if isComposing(r) {
		return &node{op: opCombOnly, r: r}
	}
	n := &node{op: opChar, r: r}
	for p.pos < len(p.src) && p.src[p.pos] != '\\' {
		pk := p.peek()
		if pk < 0 || isMagic(pk) || !isComposing(rune(pk)) {
			break
		}
		n.comb = append(n.comb, rune(pk))
		p.skip()
	}
	return n
}

func (p *parser) lastSubNode() *node {
	var items []*node
	for i := 0; i < len(p.lastSub); {
		r, size := decodeAt(p.lastSub, i)
		i += size
		n := &node{op: opChar, r: nul(r)}
		if isComposing(r) {
			if len(items) > 0 && items[len(items)-1].op == opChar {
				last := items[len(items)-1]
				last.comb = append(last.comb, r)
				continue
			}
			n = &node{op: opCombOnly, r: r}
		}
		items = append(items, n)
	}
	switch len(items) {
	case 0:
		return &node{op: opEmpty}
	case 1:
		return items[0]
	}
	return &node{op: opConcat, subs: items}
}

// seenEndBrace allows \N only after group N is closed, or when a look-behind follows.
func (p *parser) seenEndBrace(ref int) bool {
	if p.hadEnd[ref] {
		return true
	}
	if strings.Contains(p.src[p.pos:], "@<=") || strings.Contains(p.src[p.pos:], "@<!") {
		return true
	}
	p.fail("E65: Illegal back reference")
	return false
}

func (p *parser) percent(savePrevAtStart bool) *node {
	c := noMagic(p.get())
	switch c {
	case '(':
		return p.reg(regNParen)
	case '^':
		return &node{op: opBOF}
	case '$':
		return &node{op: opEOF}
	case '#':
		if p.at(p.pos) == '=' && p.at(p.pos+1) >= '0' && p.at(p.pos+1) <= '2' {
			return p.fail(`E1281: Atom '\%%#=%c' must be at the start of the pattern`, rune(p.at(p.pos+1)))
		}
		return &node{op: opCursor}
	case 'V':
		return &node{op: opVisual}
	case 'C':
		return &node{op: opSkipComb}
	case '[':
		return p.optSeq()
	case 'd', 'o', 'x', 'u', 'U':
		var i int64
		switch c {
		case 'd':
			i = p.getDec()
		case 'o':
			i = p.getOct()
		case 'x':
			i = p.getHex(2)
		case 'u':
			i = p.getHex(4)
		case 'U':
			i = p.getHex(8)
		}
		if i < 0 || i > math.MaxInt32 {
			return p.fail("E678: Invalid character after %s%%[dxouU]", bs(p.magic == magicAll))
		}
		return &node{op: opChar, r: nul(rune(i))}
	}
	if isDigit(byte(c)) && c < 128 || c == '<' || c == '>' || c == '\'' || c == '.' {
		cmp := c
		cc := c
		if cmp == '<' || cmp == '>' {
			cc = p.get()
		}
		cur := false
		if noMagic(cc) == '.' {
			cur = true
			cc = p.get()
			if cc >= '0' && cc <= '9' {
				return p.fail(`E1204: No Number allowed after .: '\%%%c'`, rune(cc))
			}
		}
		n, gotDigit := 0, false
		for cc >= '0' && cc <= '9' {
			gotDigit = true
			if n < infinite/10 {
				n = n*10 + cc - '0'
			}
			cc = p.get()
		}
		if cc == '\'' && n == 0 {
			m := noMagic(p.get())
			return &node{op: opMark, r: rune(m), cmp: byte(cmp)}
		}
		if (cc == 'l' || cc == 'c' || cc == 'v') && (cur || gotDigit) {
			k := byte(cmp)
			if k != '<' && k != '>' {
				k = 0
			}
			nd := &node{num: n, cmp: k, cur: cur}
			switch cc {
			case 'l':
				nd.op = opLnum
				if savePrevAtStart {
					p.atStart = true
				}
			case 'c':
				nd.op = opCol
			default:
				nd.op = opVcol
			}
			return nd
		}
	}
	return p.unknownOp("E867", `\%`, p.prev)
}

// optSeq parses \%[...]: each atom is optional, but only when the ones before it matched.
func (p *parser) optSeq() *node {
	var items []*node
	for {
		c := p.get()
		if c == ']' {
			break
		}
		if c == tokEOF {
			return p.fail("E69: Missing ] after %s%%[", bs(p.magic == magicAll))
		}
		p.unget()
		a := p.atom()
		if a == nil {
			return nil
		}
		items = append(items, a)
	}
	if len(items) == 0 {
		return p.fail("E70: Empty %s%%[]", bs(p.magic == magicAll))
	}
	var rest *node
	for i := len(items) - 1; i >= 0; i-- {
		seq := items[i]
		if rest != nil {
			seq = &node{op: opConcat, subs: []*node{items[i], rest}}
		}
		rest = &node{op: opRepeat, subs: []*node{seq}, min: 0, max: 1, greedy: true}
	}
	return rest
}

// markAnyComb lets a literal followed by \%C match whatever composing characters follow it.
func markAnyComb(n *node) {
	if n == nil {
		return
	}
	for i, s := range n.subs {
		markAnyComb(s)
		if n.op == opConcat && s.op == opChar && i+1 < len(n.subs) && n.subs[i+1].op == opSkipComb {
			s.anyComb = true
		}
	}
}
