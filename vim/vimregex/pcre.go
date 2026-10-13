package vimregex

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PCRE flags: the pattern options PCRE2 takes outside the pattern, as (?i), (?m), (?s), (?x) and (?n) take inside.
type PCREFlags struct {
	Caseless  bool // i
	Multiline bool // m: ^ and $ at every line
	DotAll    bool // s: . matches a newline
	Extended  bool // x: white space and # comments ignored outside classes
	ExtendedX bool // xx: and white space inside classes too
	NoAuto    bool // n: plain (…) doesn't capture
}

// UnsupportedError is a PCRE2 feature this matcher doesn't have: recursion, conditionals, verbs, callouts and the
// like. It says which.
type UnsupportedError struct{ Feature string }

func (e *UnsupportedError) Error() string { return "not supported in PCRE2 patterns here: " + e.Feature }

// CompilePCRE compiles a PCRE2 pattern for the same matcher. The subject is lines without their newlines, the last
// having none unless the text ends in one (an empty last line).
func CompilePCRE(pattern string, f PCREFlags) (*Regex, error) {
	p := &pcreParser{src: unquote(pattern), npar: 1, names: map[string]int{}, groups: map[int]*node{}, measuring: map[int]bool{}, asserts: map[*node]bool{}}
	p.flags = f
	prog, err := p.parse()
	if err != nil {
		return nil, err
	}
	for _, br := range p.backrefs {
		switch {
		case br.name != "":
			n, ok := p.names[br.name]
			if !ok {
				return nil, fmt.Errorf("reference to non-existent subpattern %q", br.name)
			}
			br.node.num = n
		case br.node.num >= p.npar:
			return nil, fmt.Errorf("reference to non-existent subpattern %d", br.node.num)
		}
	}
	return &Regex{prog: prog, ncap: p.npar, names: p.names, pcre: true}, nil
}

// unquote rewrites \Q…\E as escaped characters, so the parser, classes and ranges included, sees each quoted
// character on its own. An empty \Q\E becomes a \E, which the parser skips.
func unquote(s string) string {
	if !strings.Contains(s, `\Q`) && !strings.Contains(s, `\E`) {
		return s
	}
	var b strings.Builder
	quoting, quoted := false, false
	for i := 0; i < len(s); i++ {
		switch {
		case quoting && strings.HasPrefix(s[i:], `\E`):
			if !quoted {
				b.WriteString(`\E`) // an empty \Q\E still parts a range, as a stray \E does
			}
			quoting = false
			i++
		case quoting:
			quoted = true
			r, n := utf8.DecodeRuneInString(s[i:])
			if r < 0x80 && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				b.WriteByte('\\')
			}
			b.WriteString(s[i : i+n])
			i += n - 1
		case strings.HasPrefix(s[i:], `\Q`):
			quoting, quoted = true, false
			i++
		case s[i] == '\\' && i+1 < len(s):
			b.WriteString(s[i : i+2])
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// Names are the named groups' numbers.
func (r *Regex) Names() map[string]int { return r.names }

// Groups is how many groups the pattern has, \0 included.
func (r *Regex) Groups() int { return r.ncap }

type pcreParser struct {
	src       string
	i         int
	flags     PCREFlags
	npar      int
	names     map[string]int
	groups    map[int]*node  // each group's body, for a back reference's width
	measuring map[int]bool   // the groups widths is inside, so a reference to one in itself stops it
	inLook    int            // how many look-arounds deep the parser is
	asserts   map[*node]bool // the look-arounds written as such, which a quantifier repeats at most once
	backrefs  []backrefFix
}

type backrefFix struct {
	node *node
	name string
}

func (p *pcreParser) unsupported(what string) error { return &UnsupportedError{Feature: what} }

func (p *pcreParser) errorf(format string, args ...any) error {
	return fmt.Errorf("at offset %d: %s", p.i, fmt.Sprintf(format, args...))
}

func (p *pcreParser) parse() (*node, error) {
	n, err := p.alt()
	if err != nil {
		return nil, err
	}
	if p.i < len(p.src) {
		return nil, p.errorf("unmatched closing parenthesis")
	}
	return n, nil
}

func (p *pcreParser) more() bool { return p.i < len(p.src) }
func (p *pcreParser) peek() byte { return p.src[p.i] }
func (p *pcreParser) has(s string) bool {
	return strings.HasPrefix(p.src[p.i:], s)
}

// skipExtended skips white space and comments in extended mode.
func (p *pcreParser) skipExtended() {
	if !p.flags.Extended && !p.flags.ExtendedX {
		return
	}
	for p.more() {
		switch c := p.peek(); {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			p.i++
		case c == '#':
			for p.more() && p.peek() != '\n' {
				p.i++
			}
		default:
			return
		}
	}
}

func (p *pcreParser) alt() (*node, error) {
	var alts []*node
	for {
		c, err := p.concat()
		if err != nil {
			return nil, err
		}
		alts = append(alts, c)
		if !p.more() || p.peek() != '|' {
			break
		}
		p.i++
	}
	if len(alts) == 1 {
		return alts[0], nil
	}
	return &node{op: opAlt, subs: alts}, nil
}

func (p *pcreParser) concat() (*node, error) {
	var seq []*node
	for {
		p.skipExtended()
		if !p.more() || p.peek() == '|' || p.peek() == ')' {
			break
		}
		if p.has(`\E`) {
			p.i += 2
			continue
		}
		if p.has("(?#") {
			end := strings.IndexByte(p.src[p.i:], ')')
			if end < 0 {
				return nil, p.errorf("missing ) after (?# comment")
			}
			p.i += end + 1
			continue
		}
		if c := p.peek(); len(seq) > 0 && (c == '*' || c == '+' || c == '?' || c == '{' && validBrace(p.src[p.i:])) {
			// After a comment or (?i), a quantifier still applies to the atom before them.
			q, err := p.quantify(seq[len(seq)-1])
			if err != nil {
				return nil, err
			}
			seq[len(seq)-1] = q
			continue
		}
		atom, err := p.atom()
		if err != nil {
			return nil, err
		}
		if atom == nil {
			continue
		}
		atom, err = p.quantify(atom)
		if err != nil {
			return nil, err
		}
		seq = append(seq, atom)
	}
	switch len(seq) {
	case 0:
		return &node{op: opEmpty}, nil
	case 1:
		return seq[0], nil
	}
	return &node{op: opConcat, subs: seq}, nil
}

func (p *pcreParser) char(r rune) *node {
	if r == '\n' {
		return &node{op: opNL}
	}
	return &node{op: opChar, r: r, fold: p.flags.Caseless && unicode.SimpleFold(r) != r}
}

// quantify applies a quantifier after atom, if there is one: greedy, lazy (?) or possessive (+).
func (p *pcreParser) quantify(atom *node) (*node, error) {
	repeated := false
	for {
		p.skipExtended()
		if !p.more() {
			return atom, nil
		}
		lo, hi := 0, 0
		switch c := p.peek(); c {
		case '*':
			lo, hi = 0, infinite
			p.i++
		case '+':
			lo, hi = 1, infinite
			p.i++
		case '?':
			lo, hi = 0, 1
			p.i++
		case '{':
			l, h, n, ok, err := braceQuantifierErr(p.src[p.i:])
			if err != nil {
				return nil, p.errorf("%v", err)
			}
			if !ok {
				return atom, nil
			}
			lo, hi = l, h
			p.i += n
		default:
			return atom, nil
		}
		if repeated {
			return nil, p.errorf("quantifier does not follow a repeatable item")
		}
		repeated = true
		switch atom.op {
		case opBOF, opPBOL, opEOL, opSubjEnd, opSubjEndAbs, opWordB, opNotWordB, opStartMatch, opZS:
			return nil, p.errorf("quantifier does not follow a repeatable item")
		}
		if p.asserts[atom] {
			// An assertion repeats at most once: more would assert the same thing again.
			lo, hi = min(lo, 1), min(hi, 1)
		}
		greedy, possessive := true, false
		p.skipExtended()
		if p.more() {
			switch p.peek() {
			case '?':
				greedy = false
				p.i++
			case '+':
				possessive = true
				p.i++
			}
		}
		rep := &node{op: opRepeat, subs: []*node{atom}, min: lo, max: hi, greedy: greedy}
		if possessive {
			rep = &node{op: opAtomic, subs: []*node{rep}}
		}
		atom = rep
	}
}

func validBrace(s string) bool { _, _, _, ok := braceQuantifier(s); return ok }

// braceQuantifier reads {n}, {n,}, {n,m} or {,m}; anything else is literal. One with numbers out of order or too
// big is a quantifier, and an error.
func braceQuantifier(s string) (lo, hi, n int, ok bool) {
	lo, hi, n, ok, _ = braceQuantifierErr(s)
	return
}

func braceQuantifierErr(s string) (lo, hi, n int, ok bool, err error) {
	end := strings.IndexByte(s, '}')
	if end < 0 {
		return 0, 0, 0, false, nil
	}
	body := strings.ReplaceAll(s[1:end], " ", "")
	a, b, comma := strings.Cut(body, ",")
	num := func(x string) (int, bool) {
		if x == "" {
			return 0, false
		}
		for _, c := range x {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		v, err := strconv.Atoi(x)
		if err != nil {
			return 1 << 30, true // all digits, too many of them
		}
		return v, true
	}
	tooBig := func(v int) error {
		if v > 65535 {
			return fmt.Errorf("number too big in {} quantifier")
		}
		return nil
	}
	switch {
	case !comma:
		v, ok := num(a)
		if !ok {
			return 0, 0, 0, false, nil
		}
		return v, v, end + 1, true, tooBig(v)
	case a == "" && b == "":
		return 0, 0, 0, false, nil
	case a == "":
		v, ok := num(b)
		if !ok {
			return 0, 0, 0, false, nil
		}
		return 0, v, end + 1, true, tooBig(v)
	case b == "":
		v, ok := num(a)
		if !ok {
			return 0, 0, 0, false, nil
		}
		return v, infinite, end + 1, true, tooBig(v)
	}
	x, ok1 := num(a)
	y, ok2 := num(b)
	if !ok1 || !ok2 {
		return 0, 0, 0, false, nil
	}
	if err := cmpErr(tooBig(x), tooBig(y)); err != nil {
		return 0, 0, 0, true, err
	}
	if x > y {
		return 0, 0, 0, true, fmt.Errorf("numbers out of order in {} quantifier")
	}
	return x, y, end + 1, true, nil
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

func (p *pcreParser) atom() (*node, error) {
	c := p.peek()
	switch c {
	case '(':
		return p.group()
	case '[':
		return p.class()
	case '.':
		p.i++
		return &node{op: opAny, nl: p.flags.DotAll}, nil
	case '^':
		p.i++
		if p.flags.Multiline {
			return &node{op: opPBOL}, nil
		}
		return &node{op: opBOF}, nil
	case '$':
		p.i++
		if p.flags.Multiline {
			return &node{op: opEOL}, nil
		}
		return &node{op: opSubjEnd}, nil
	case '\\':
		return p.escape()
	case '*', '+', '?':
		return nil, p.errorf("quantifier does not follow a repeatable item")
	case '{':
		if validBrace(p.src[p.i:]) {
			return nil, p.errorf("quantifier does not follow a repeatable item")
		}
	}
	r, n := utf8.DecodeRuneInString(p.src[p.i:])
	p.i += n
	return p.char(r), nil
}

// group parses ( … ) in all its forms.
func (p *pcreParser) group() (*node, error) {
	p.i++ // (
	if p.has("*") {
		return nil, p.unsupported("backtracking control verbs")
	}
	saved := p.flags
	defer func() { p.flags = saved }()
	capIdx, name := -1, ""
	wrap := opEmpty
	switch {
	case p.has("?:"):
		p.i += 2
	case p.has("?>"):
		p.i += 2
		wrap = opAtomic
	case p.has("?="):
		p.i += 2
		wrap = opAhead
	case p.has("?!"):
		p.i += 2
		wrap = opNotAhead
	case p.has("?<="):
		p.i += 3
		wrap = opBehind
	case p.has("?<!"):
		p.i += 3
		wrap = opNotBehind
	case p.has("?*") || p.has("?<*"):
		return nil, p.unsupported("non-atomic assertions")
	case p.has("?<") || p.has("?'") || p.has("?P<"):
		if p.has("?P<") {
			p.i += 3
		} else {
			p.i += 2
		}
		closer := byte('>')
		if p.src[p.i-1] == '\'' {
			closer = '\''
		}
		end := strings.IndexByte(p.src[p.i:], closer)
		if end <= 0 {
			return nil, p.errorf("syntax error in subpattern name")
		}
		name = p.src[p.i : p.i+end]
		if err := p.checkName(name); err != nil {
			return nil, err
		}
		p.i += end + 1
		if _, dup := p.names[name]; dup {
			return nil, p.unsupported("duplicate group names")
		}
		capIdx = p.npar
		p.names[name] = capIdx
		p.npar++
	case p.has("?P="):
		p.i += 3
		end := strings.IndexByte(p.src[p.i:], ')')
		if end <= 0 {
			return nil, p.errorf("syntax error in subpattern name")
		}
		n := &node{op: opBackref, fold: p.flags.Caseless}
		p.backrefs = append(p.backrefs, backrefFix{n, p.src[p.i : p.i+end]})
		p.i += end + 1
		return n, nil
	case p.has("?|"):
		return nil, p.unsupported("branch reset groups")
	case p.has("?("):
		return nil, p.unsupported("conditional groups")
	case p.has("?R") || p.has("?&") || p.has("?P>") || p.has("?+") || p.has("?-") && len(p.src) > p.i+2 && isDigit(p.src[p.i+2]) ||
		p.has("?") && len(p.src) > p.i+1 && isDigit(p.src[p.i+1]):
		return nil, p.unsupported("recursion and subroutine calls")
	case p.has("?C"):
		return nil, p.unsupported("callouts")
	case p.has("?"):
		// Option setting: (?imnsx-imnsx) or (?imnsx-imnsx: … ).
		p.i++
		on, caret := true, false
		for p.more() && p.peek() != ')' && p.peek() != ':' {
			switch p.peek() {
			case '-':
				if !on || caret {
					return nil, p.errorf("invalid hyphen in option setting")
				}
				on = false
			case '^':
				p.flags = PCREFlags{}
				caret = true
			case 'i':
				p.flags.Caseless = on
			case 'm':
				p.flags.Multiline = on
			case 's':
				p.flags.DotAll = on
			case 'n':
				p.flags.NoAuto = on
			case 'x':
				n := 1
				for p.i+1 < len(p.src) && p.src[p.i+1] == 'x' {
					p.i++
					n++
				}
				p.flags.Extended, p.flags.ExtendedX = on, on && n > 1
			case 'J', 'U':
				return nil, p.unsupported("(?" + string(p.peek()) + ")")
			default:
				return nil, p.errorf("unrecognized character after (? or (?-")
			}
			p.i++
		}
		if !p.more() {
			return nil, p.errorf("missing closing parenthesis")
		}
		if p.peek() == ')' {
			// The options hold to the end of the enclosing group.
			p.i++
			saved = p.flags
			return nil, nil
		}
		p.i++ // :
	default:
		if !p.flags.NoAuto {
			capIdx = p.npar
			p.npar++
		}
	}
	look := wrap == opAhead || wrap == opNotAhead || wrap == opBehind || wrap == opNotBehind
	if look {
		p.inLook++
	}
	body, err := p.alt()
	if look {
		p.inLook--
	}
	if err != nil {
		return nil, err
	}
	if !p.more() || p.peek() != ')' {
		return nil, p.errorf("missing closing parenthesis")
	}
	p.i++
	if capIdx >= 0 {
		p.groups[capIdx] = body
		body = &node{op: opCapture, num: capIdx, subs: []*node{body}}
	}
	switch wrap {
	case opEmpty:
		if p.asserts[body] {
			// (?:(?=…)) is a group, which repeats as any group does.
			return &node{op: opConcat, subs: []*node{body}}, nil
		}
		return body, nil
	case opBehind, opNotBehind:
		lo, hi, ok := p.widths(body)
		switch {
		case !ok:
			return nil, p.errorf("lookbehind assertion is not fixed length")
		case hi > 65535:
			return nil, p.errorf("lookbehind assertion is too long")
		case lo != hi && hi > 255:
			return nil, p.errorf("branch too long in variable-length lookbehind assertion")
		}
		n := &node{op: wrap, num: hi, subs: []*node{body}}
		p.asserts[n] = true
		return n, nil
	}
	n := &node{op: wrap, subs: []*node{body}}
	if look {
		p.asserts[n] = true
	}
	return n, nil
}

// widths is the fewest and most characters n can match, if the most is bounded, as a lookbehind needs. A back
// reference is as wide as its group, when that's fixed.
func (p *pcreParser) widths(n *node) (lo, hi int, ok bool) {
	switch n.op {
	case opChar, opAny, opClass, opSet, opNL:
		return 1, 1, true
	case opCapture, opAtomic:
		return p.widths(n.subs[0])
	case opConcat:
		for _, s := range n.subs {
			l, h, ok := p.widths(s)
			if !ok {
				return 0, 0, false
			}
			lo, hi = lo+l, hi+h
		}
		return lo, hi, true
	case opAlt:
		lo = infinite
		for _, s := range n.subs {
			l, h, ok := p.widths(s)
			if !ok {
				return 0, 0, false
			}
			lo, hi = min(lo, l), max(hi, h)
		}
		return lo, hi, true
	case opRepeat:
		l, h, ok := p.widths(n.subs[0])
		if ok && h == 0 {
			return 0, 0, true
		}
		if n.max == infinite || !ok {
			return 0, 0, false
		}
		return l * n.min, h * n.max, true
	case opBackref:
		g, ok := p.groups[n.num]
		if !ok || p.measuring[n.num] {
			return 0, 0, false
		}
		p.measuring[n.num] = true
		l, h, ok := p.widths(g)
		delete(p.measuring, n.num)
		return l, h, ok && l == h
	}
	return 0, 0, true
}

// pcreClass is one of \d \D \w \W \s \S \h \H \v \V \N as a set.
func pcreClass(c byte) (*charSet, bool, bool) {
	s := &charSet{}
	neg := false
	switch c {
	case 'd', 'D':
		s.addRange('0', '9')
	case 'w', 'W':
		s.addRange('a', 'z')
		s.addRange('A', 'Z')
		s.addRange('0', '9')
		s.add('_')
	case 's', 'S':
		for _, r := range "\t\v\f\r " {
			s.add(r)
		}
		s.nl = true
	case 'h', 'H':
		for _, r := range "\t   ᠎  　" {
			s.add(r)
		}
		s.addRange(0x2000, 0x200a)
	case 'v', 'V':
		for _, r := range "\v\f\r\u0085  " {
			s.add(r)
		}
		s.nl = true
	default:
		return nil, false, false
	}
	if c >= 'A' && c <= 'Z' {
		neg = true
	}
	return s, neg, true
}

// negate makes a set its complement, newline included.
func negated(s *charSet, fold bool) *node {
	return &node{op: opSet, set: &charSet{ranges: s.ranges, classes: s.classes, nl: false}, neg: true, fold: fold,
		nl: !s.nl}
}

func (p *pcreParser) escape() (*node, error) {
	p.i++ // backslash
	if !p.more() {
		return nil, p.errorf("\\ at end of pattern")
	}
	c := p.peek()
	p.i++
	if s, neg, ok := pcreClass(c); ok {
		if neg {
			return negated(s, false), nil
		}
		return &node{op: opSet, set: s}, nil
	}
	switch c {
	case 'b':
		return &node{op: opWordB}, nil
	case 'B':
		return &node{op: opNotWordB}, nil
	case 'A':
		return &node{op: opBOF}, nil
	case 'Z':
		return &node{op: opSubjEnd}, nil
	case 'z':
		return &node{op: opSubjEndAbs}, nil
	case 'G':
		return &node{op: opStartMatch}, nil
	case 'K':
		if p.inLook > 0 {
			return nil, p.errorf(`\K is not allowed in lookarounds`)
		}
		return &node{op: opZS}, nil
	case 'N':
		if p.has("{") {
			return nil, p.unsupported(`\N{U+…} outside UTF mode`)
		}
		return &node{op: opAny}, nil
	case 'R':
		alts := []*node{{op: opConcat, subs: []*node{{op: opChar, r: '\r'}, {op: opNL}}}, {op: opNL}}
		for _, r := range "\v\f\r\u0085  " {
			alts = append(alts, &node{op: opChar, r: r})
		}
		return &node{op: opAtomic, subs: []*node{{op: opAlt, subs: alts}}}, nil
	case 'X':
		return nil, p.unsupported(`\X`)
	case 'C':
		return nil, p.unsupported(`\C`)
	case 'p', 'P':
		set, neg, err := p.property(c == 'P')
		if err != nil {
			return nil, err
		}
		if neg {
			return negated(set, false), nil
		}
		return &node{op: opSet, set: set}, nil
	case 'g':
		return p.gref()
	case 'k':
		closer := map[byte]byte{'<': '>', '\'': '\'', '{': '}'}
		if !p.more() || closer[p.peek()] == 0 {
			return nil, p.errorf(`\k is not followed by a name`)
		}
		end := strings.IndexByte(p.src[p.i+1:], closer[p.peek()])
		if end <= 0 {
			return nil, p.errorf("syntax error in subpattern name")
		}
		n := &node{op: opBackref, fold: p.flags.Caseless}
		name := strings.TrimSpace(p.src[p.i+1 : p.i+1+end])
		n.num = p.names[name]
		p.backrefs = append(p.backrefs, backrefFix{n, name})
		p.i += end + 2
		return n, nil
	}
	if c >= '1' && c <= '9' {
		// A back reference, unless it's more than 9 and more groups than there are: then octal.
		j := p.i
		for j < len(p.src) && isDigit(p.src[j]) {
			j++
		}
		num, _ := strconv.Atoi(p.src[p.i-1 : j])
		if num < 10 || num < p.npar || c >= '8' {
			p.i = j
			n := &node{op: opBackref, num: num, fold: p.flags.Caseless}
			p.backrefs = append(p.backrefs, backrefFix{node: n})
			return n, nil
		}
	}
	p.i -= 2
	r, err := p.literalEscape(false)
	if err != nil {
		return nil, err
	}
	return p.char(r), nil
}

// gref is \g: \gN, \g{N}, \g{-N}, \g{name}, \g-N; \g<…> is a subroutine call.
func (p *pcreParser) gref() (*node, error) {
	if p.has("<") || p.has("'") {
		return nil, p.unsupported("subroutine calls")
	}
	var ref string
	if p.has("{") {
		end := strings.IndexByte(p.src[p.i:], '}')
		if end < 0 {
			return nil, p.errorf(`\g is not followed by a braced, angle-bracketed, or quoted name/number`)
		}
		ref = strings.TrimSpace(p.src[p.i+1 : p.i+end])
		p.i += end + 1
	} else {
		j := p.i
		if j < len(p.src) && (p.src[j] == '-' || p.src[j] == '+') {
			j++
		}
		for j < len(p.src) && isDigit(p.src[j]) {
			j++
		}
		ref = p.src[p.i:j]
		p.i = j
	}
	if ref == "" {
		return nil, p.errorf(`\g is not followed by a braced, angle-bracketed, or quoted name/number or by a plain number`)
	}
	n := &node{op: opBackref, fold: p.flags.Caseless}
	if num, err := strconv.Atoi(ref); err == nil {
		switch {
		case ref[0] == '-':
			num = p.npar + num
		case ref[0] == '+':
			num = p.npar + num - 1
		}
		if num <= 0 {
			return nil, p.errorf("a relative value of zero is not allowed")
		}
		n.num = num
		p.backrefs = append(p.backrefs, backrefFix{node: n})
		return n, nil
	}
	p.backrefs = append(p.backrefs, backrefFix{n, ref})
	return n, nil
}

// literalEscape reads an escape that stands for one character, at the backslash: in a class, \b is a backspace.
func (p *pcreParser) literalEscape(inClass bool) (rune, error) {
	p.i++
	if !p.more() {
		return 0, p.errorf("\\ at end of pattern")
	}
	c := p.peek()
	p.i++
	switch c {
	case 'a':
		return 7, nil
	case 'e':
		return 0x1b, nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'b':
		if inClass {
			return '\b', nil
		}
	case '0':
		v := 0
		for k := 0; k < 2 && p.more() && p.peek() >= '0' && p.peek() <= '7'; k++ {
			v = v*8 + int(p.peek()-'0')
			p.i++
		}
		return rune(v), nil
	case 'o':
		if !p.has("{") {
			return 0, p.errorf(`missing opening brace after \o`)
		}
		end := strings.IndexByte(p.src[p.i:], '}')
		v, err := strconv.ParseUint(p.src[p.i+1:p.i+end], 8, 32)
		if end < 0 || err != nil {
			return 0, p.errorf(`non-octal character in \o{}`)
		}
		p.i += end + 1
		return rune(v), nil
	case 'x':
		if p.has("{") {
			end := strings.IndexByte(p.src[p.i:], '}')
			if end < 0 {
				return 0, p.errorf(`missing } after \x{`)
			}
			v, err := strconv.ParseUint(p.src[p.i+1:p.i+end], 16, 32)
			if err != nil {
				return 0, p.errorf(`non-hex character in \x{}`)
			}
			p.i += end + 1
			return rune(v), nil
		}
		v := 0
		for k := 0; k < 2 && p.more() && isHexByte(p.peek()); k++ {
			v = v*16 + hexVal(p.peek())
			p.i++
		}
		return rune(v), nil
	case 'c':
		if !p.more() || p.peek() >= 0x80 {
			return 0, p.errorf(`\c at end of pattern`)
		}
		ch := p.peek()
		p.i++
		if ch >= 'a' && ch <= 'z' {
			ch -= 32
		}
		return rune(ch ^ 0x40), nil
	}
	if c == '8' || c == '9' || inClass && (c == 'g' || c == 'k') {
		return rune(c), nil
	}
	if c >= '1' && c <= '7' {
		v := int(c - '0')
		for k := 0; k < 2 && p.more() && p.peek() >= '0' && p.peek() <= '7'; k++ {
			v = v*8 + int(p.peek()-'0')
			p.i++
		}
		return rune(v), nil
	}
	if c >= 0x80 || unicode.IsLetter(rune(c)) || isDigit(c) {
		if c < 0x80 {
			return 0, p.errorf(`unrecognized character follows \`)
		}
		r, n := utf8.DecodeRuneInString(p.src[p.i-1:])
		p.i += n - 1
		return r, nil
	}
	return rune(c), nil
}

// checkName checks a group name: word characters, not starting with a digit, at most 128 of them.
func (p *pcreParser) checkName(name string) error {
	if name[0] >= '0' && name[0] <= '9' {
		return p.errorf("subpattern name must start with a non-digit")
	}
	for _, r := range name {
		if !pcreWord(r) && !unicode.IsLetter(r) {
			return p.errorf("syntax error in subpattern name (missing terminator?)")
		}
	}
	if len(name) > 128 {
		return p.errorf("subpattern name is too long (maximum 128 code units)")
	}
	return nil
}

func wordSet() *node {
	s, _, _ := pcreClass('w')
	return &node{op: opSet, set: s}
}

func isHexByte(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// property reads \p{…} or \pL: a Unicode general category or script.
func (p *pcreParser) property(neg bool) (*charSet, bool, error) {
	var name string
	if p.has("{") {
		end := strings.IndexByte(p.src[p.i:], '}')
		if end < 0 {
			return nil, false, p.errorf(`malformed \P or \p sequence`)
		}
		name = p.src[p.i+1 : p.i+end]
		p.i += end + 1
	} else if p.more() {
		name = p.src[p.i : p.i+1]
		p.i++
	}
	if strings.HasPrefix(name, "^") {
		neg, name = !neg, name[1:]
	}
	name = strings.ReplaceAll(strings.ReplaceAll(name, " ", ""), "_", "")
	var tab *unicode.RangeTable
	switch {
	case name == "Any":
		tab = &unicode.RangeTable{R32: []unicode.Range32{{Lo: 0, Hi: unicode.MaxRune, Stride: 1}}}
	case name == "L&" || name == "LC":
		tab = mergeTables(unicode.Lu, unicode.Ll, unicode.Lt)
	default:
		for k, t := range unicode.Categories {
			if strings.EqualFold(k, name) {
				tab = t
			}
		}
		for k, t := range unicode.Scripts {
			if tab == nil && strings.EqualFold(strings.ReplaceAll(k, "_", ""), name) {
				tab = t
			}
		}
	}
	if tab == nil {
		return nil, false, p.errorf("unknown property name after \\P or \\p")
	}
	s := &charSet{}
	for _, r := range tab.R16 {
		for lo := rune(r.Lo); lo <= rune(r.Hi); lo += rune(r.Stride) {
			if r.Stride == 1 {
				s.addRange(lo, rune(r.Hi))
				break
			}
			s.add(lo)
		}
	}
	for _, r := range tab.R32 {
		for lo := rune(r.Lo); lo <= rune(r.Hi); lo += rune(r.Stride) {
			if r.Stride == 1 {
				s.addRange(lo, rune(r.Hi))
				break
			}
			s.add(lo)
		}
	}
	return s, neg, nil
}

func mergeTables(ts ...*unicode.RangeTable) *unicode.RangeTable {
	out := &unicode.RangeTable{}
	for _, t := range ts {
		out.R16 = append(out.R16, t.R16...)
		out.R32 = append(out.R32, t.R32...)
	}
	return out
}

var posixClasses = map[string]func(s *charSet){
	"alpha":  func(s *charSet) { s.addRange('a', 'z'); s.addRange('A', 'Z') },
	"digit":  func(s *charSet) { s.addRange('0', '9') },
	"alnum":  func(s *charSet) { s.addRange('a', 'z'); s.addRange('A', 'Z'); s.addRange('0', '9') },
	"lower":  func(s *charSet) { s.addRange('a', 'z') },
	"upper":  func(s *charSet) { s.addRange('A', 'Z') },
	"space":  func(s *charSet) { s.addRange('\t', '\r'); s.add(' '); s.nl = true },
	"blank":  func(s *charSet) { s.add(' '); s.add('\t') },
	"xdigit": func(s *charSet) { s.addRange('0', '9'); s.addRange('a', 'f'); s.addRange('A', 'F') },
	"punct": func(s *charSet) {
		s.addRange('!', '/')
		s.addRange(':', '@')
		s.addRange('[', '`')
		s.addRange('{', '~')
	},
	"cntrl": func(s *charSet) { s.addRange(0, 9); s.addRange(11, 0x1f); s.add(0x7f); s.nl = true },
	"graph": func(s *charSet) { s.addRange('!', '~') },
	"print": func(s *charSet) { s.addRange(' ', '~') },
	"word":  func(s *charSet) { s.addRange('a', 'z'); s.addRange('A', 'Z'); s.addRange('0', '9'); s.add('_') },
	"ascii": func(s *charSet) { s.addRange(0, 9); s.addRange(11, 0x7f); s.nl = true },
}

// posixSyntax reports whether s at i (a :, . or =, after a [) starts POSIX class syntax, as pcre2_compile.c's
// check_posix_syntax decides it, and where its closing terminator is.
func posixSyntax(s string, i int) (int, bool) {
	term := s[i]
	for j := i + 1; len(s)-j >= 2; j++ {
		switch {
		case s[j] == '\\' && (s[j+1] == ']' || s[j+1] == '\\'):
			j++
		case s[j] == '[' && s[j+1] == term || s[j] == ']':
			return 0, false
		case s[j] == term && s[j+1] == ']':
			return j, true
		}
	}
	return 0, false
}

// class parses [ … ]: characters, ranges, escapes, \d-style classes, POSIX [:name:] and [:^name:].
func (p *pcreParser) class() (*node, error) {
	start := p.i
	switch {
	case p.has("[[:<:]]"):
		p.i += 7
		return &node{op: opConcat, subs: []*node{{op: opWordB}, {op: opAhead, subs: []*node{wordSet()}}}}, nil
	case p.has("[[:>:]]"):
		p.i += 7
		return &node{op: opConcat, subs: []*node{{op: opWordB}, {op: opBehind, num: 1, subs: []*node{wordSet()}}}}, nil
	}
	if len(p.src) > p.i+2 && strings.IndexByte(":.=", p.src[p.i+1]) >= 0 {
		// [:x:] on its own is a POSIX class outside a class.
		if _, ok := posixSyntax(p.src, p.i+1); ok {
			if p.src[p.i+1] == ':' {
				return nil, p.errorf("POSIX named classes are supported only within a class")
			}
			return nil, p.errorf("POSIX collating elements are not supported")
		}
	}
	p.i++ // [
	neg := false
	for p.has(`\E`) {
		p.i += 2
	}
	if p.more() && p.peek() == '^' {
		neg = true
		p.i++
	}
	set := &charSet{}
	// Negated classes inside the class ([\D], [[:^alpha:]]) are unions with complements: kept as sub-sets.
	var subs []*node
	first := true
	prev := rune(-1) // the last single character; -2 after a class like \d, which no range may follow
	for {
		if p.flags.ExtendedX {
			for p.more() && (p.peek() == ' ' || p.peek() == '\t') {
				p.i++
			}
		}
		if !p.more() {
			p.i = start
			return nil, p.errorf("missing terminating ] for character class")
		}
		c := p.peek()
		if c == ']' && !first {
			p.i++
			break
		}
		if p.has(`\E`) {
			p.i += 2
			if prev == -2 {
				prev = -1 // after a class, \E makes a - literal
			}
			continue
		}
		first = false
		if c == '-' && prev == -2 && p.i+1 < len(p.src) && p.src[p.i+1] != ']' {
			return nil, p.errorf("invalid range in character class")
		}
		if c == '[' && (p.has("[:") || p.has("[=") || p.has("[.")) {
			end := -1
			if e, ok := posixSyntax(p.src, p.i+1); ok {
				end = e - (p.i + 2)
			}
			if end >= 0 {
				name := p.src[p.i+2 : p.i+2+end]
				if p.src[p.i+1] != ':' {
					return nil, p.errorf("POSIX collating elements are not supported")
				}
				p.i += end + 4
				cneg := strings.HasPrefix(name, "^")
				fill, ok := posixClasses[strings.TrimPrefix(name, "^")]
				if !ok {
					return nil, p.errorf("unknown POSIX class name")
				}
				cs := &charSet{}
				fill(cs)
				if cneg {
					subs = append(subs, negated(cs, false))
				} else {
					set.ranges = append(set.ranges, cs.ranges...)
					set.nl = set.nl || cs.nl
				}
				prev = -2
				continue
			}
		}
		var r rune
		if c == '\\' {
			if p.i+1 < len(p.src) {
				if cs, cneg, ok := pcreClass(p.src[p.i+1]); ok {
					p.i += 2
					if cneg {
						subs = append(subs, negated(cs, false))
					} else {
						set.ranges = append(set.ranges, cs.ranges...)
						set.nl = set.nl || cs.nl
					}
					prev = -2
					continue
				}
				if q := p.src[p.i+1]; q == 'p' || q == 'P' {
					p.i += 2
					cs, cneg, err := p.property(q == 'P')
					if err != nil {
						return nil, err
					}
					if cneg {
						subs = append(subs, negated(cs, false))
					} else {
						set.ranges = append(set.ranges, cs.ranges...)
					}
					prev = -2
					continue
				}
			}
			var err error
			if r, err = p.literalEscape(true); err != nil {
				return nil, err
			}
		} else {
			var n int
			r, n = utf8.DecodeRuneInString(p.src[p.i:])
			p.i += n
		}
		for p.has(`\E`) {
			p.i += 2
		}
		// A range: r - hi, where the - isn't last.
		if p.more() && p.peek() == '-' && p.i+1 < len(p.src) && p.src[p.i+1] != ']' {
			p.i++
			for p.has(`\E`) {
				p.i += 2
			}
			if p.more() && p.peek() == ']' {
				set.add(r)
				set.add('-')
				continue
			}
			var hi rune
			if p.peek() == '\\' {
				if p.i+1 < len(p.src) {
					if _, _, isCls := pcreClass(p.src[p.i+1]); isCls {
						return nil, p.errorf("invalid range in character class")
					}
				}
				var err error
				if hi, err = p.literalEscape(true); err != nil {
					return nil, err
				}
			} else if p.has("[:") {
				return nil, p.errorf("invalid range in character class")
			} else {
				var n int
				hi, n = utf8.DecodeRuneInString(p.src[p.i:])
				p.i += n
			}
			if hi < r {
				return nil, p.errorf("range out of order in character class")
			}
			set.addRange(r, hi)
			if r <= '\n' && hi >= '\n' {
				set.nl = true
			}
			prev = -1
			continue
		}
		if r == '\n' {
			set.nl = true
		}
		set.add(r)
		prev = r
	}
	_ = prev
	fold := p.flags.Caseless
	var n *node
	if len(subs) == 0 {
		n = &node{op: opSet, set: set, fold: fold}
		if neg {
			n = &node{op: opSet, set: set, neg: true, fold: fold, nl: !set.nl}
		} else {
			n.nl = set.nl
		}
		return n, nil
	}
	// A union of the set and the negated parts: an alternation, matched as one character.
	alts := []*node{{op: opSet, set: set, fold: fold, nl: set.nl}}
	for _, s := range subs {
		s.fold = fold
		alts = append(alts, s)
	}
	union := &node{op: opAlt, subs: alts}
	if neg {
		return &node{op: opConcat, subs: []*node{{op: opNotAhead, subs: []*node{union}}, {op: opAny, nl: true}}}, nil
	}
	return union, nil
}

// SubstituteError is a replacement string PCRE2 rejects, with where.
type SubstituteError struct {
	Code   int
	Offset int
	Msg    string
}

func (e *SubstituteError) Error() string {
	return fmt.Sprintf("error %d at offset %d in replacement: %s", e.Code, e.Offset, e.Msg)
}

// ExpandPCRE is a replacement string as PCRE2's pcre2_substitute reads it: $n, ${n}, $name, ${name} and $$. group
// gives a group's text by number, and whether it's set; name finds a group's number.
func ExpandPCRE(repl string, group func(i int) (string, bool), name func(string) (int, bool)) (string, error) {
	var b strings.Builder
	for i := 0; i < len(repl); i++ {
		if repl[i] != '$' {
			b.WriteByte(repl[i])
			continue
		}
		i++
		if i >= len(repl) {
			return "", &SubstituteError{-35, i, "invalid replacement string"}
		}
		if repl[i] == '$' {
			b.WriteByte('$')
			continue
		}
		braced := repl[i] == '{'
		if braced {
			i++
		}
		j := i
		digits := j < len(repl) && isDigit(repl[j])
		for j < len(repl) && (digits && isDigit(repl[j]) || !digits && pcreWord(rune(repl[j]))) {
			j++
		}
		ref := repl[i:j]
		if ref == "" {
			return "", &SubstituteError{-35, i, "invalid replacement string"}
		}
		if len(ref) > 32 {
			return "", &SubstituteError{-35, i + 32, "invalid replacement string"}
		}
		if braced {
			if j >= len(repl) || repl[j] != '}' {
				return "", &SubstituteError{-58, j, "expected closing curly bracket in replacement string"}
			}
			j++
		}
		num := -1
		if isDigit(ref[0]) {
			n, err := strconv.Atoi(ref)
			if err != nil {
				return "", &SubstituteError{-49, j, "unknown substring"}
			}
			num = n
		} else if n, ok := name(ref); ok {
			num = n
		} else {
			return "", &SubstituteError{-49, j, "unknown substring"}
		}
		text, set := group(num)
		if !set {
			if num < 0 {
				return "", &SubstituteError{-49, j, "unknown substring"}
			}
			return "", &SubstituteError{-55, j, "requested value is not set"}
		}
		b.WriteString(text)
		i = j - 1
	}
	return b.String(), nil
}
