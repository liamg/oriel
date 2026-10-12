package toml

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// keyval is where a key and its value sit in the source.
type keyval struct {
	path               []string // from the document's root
	lineStart, lineEnd int      // the whole expression, newline included; not set for members
	keyStart           int
	valStart, valEnd   int
	section            int  // index into Document.headers, or -1 for the root
	member             bool // inside an inline table
	aot                bool // under an array of tables, so a path can't name it
}

// header is a [table] or [[array of tables]] line.
type header struct {
	path               []string
	lineStart, lineEnd int
	aot                bool
}

// Kinds of table, which decide how a table may be extended (toml.md, "Table").
type tkind int

const (
	tImplicit tkind = iota // created as the parent of a [header]
	tHeader                // defined by a [header], or an element of an array of tables
	tDotted                // created by a dotted key
)

type table struct {
	kind tkind
	vals map[string]any // *table, *aot, or a value (inline tables and arrays are values, so they can't be extended)
}

type aot struct{ elems []*table }

func newTable(k tkind) *table { return &table{kind: k, vals: map[string]any{}} }

type parser struct {
	src     []byte
	pos     int
	root    *table
	keyvals []keyval
	headers []header
}

func parse(src []byte) (*parser, error) {
	if !utf8.Valid(src) {
		off := 0
		for off < len(src) {
			r, n := utf8.DecodeRune(src[off:])
			if r == utf8.RuneError && n <= 1 {
				break
			}
			off += n
		}
		return nil, errAt(src, off, "invalid UTF-8")
	}
	p := &parser{src: src, root: newTable(tHeader)}
	cur, section, inAoT := p.root, -1, false
	for p.pos < len(src) {
		lineStart := p.pos
		kv, hd := -1, -1
		p.ws()
		switch c := p.peek(); {
		case c == '#' || c == '\n' || c == '\r':
		case c == '[':
			path, isAoT, err := p.header()
			if err != nil {
				return nil, err
			}
			t, through, err := p.openTable(lineStart, path, isAoT)
			if err != nil {
				return nil, err
			}
			cur, section, inAoT = t, len(p.headers), isAoT || through
			hd = len(p.headers)
			p.headers = append(p.headers, header{path: path, lineStart: lineStart, aot: inAoT})
		default:
			keyStart := p.pos
			key, err := p.key()
			if err != nil {
				return nil, err
			}
			var base []string
			if section >= 0 {
				base = p.headers[section].path
			}
			abs := append(slices.Clone(base), key...)
			valStart, val, err := p.keyvalRest(abs, !inAoT)
			if err != nil {
				return nil, err
			}
			if err := p.define(cur, key, val, keyStart); err != nil {
				return nil, err
			}
			kv = len(p.keyvals)
			p.keyvals = append(p.keyvals, keyval{path: abs, lineStart: lineStart, keyStart: keyStart, valStart: valStart, valEnd: p.pos, section: section, aot: inAoT})
		}
		if err := p.eol(); err != nil {
			return nil, err
		}
		if kv >= 0 {
			p.keyvals[kv].lineEnd = p.pos
		}
		if hd >= 0 {
			p.headers[hd].lineEnd = p.pos
		}
	}
	return p, nil
}

func (p *parser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *parser) at(s string) bool {
	return strings.HasPrefix(string(p.src[p.pos:min(p.pos+len(s), len(p.src))]), s)
}

func (p *parser) fail(format string, args ...any) error { return errAt(p.src, p.pos, format, args...) }

func (p *parser) ws() {
	for p.pos < len(p.src) && (p.src[p.pos] == ' ' || p.src[p.pos] == '\t') {
		p.pos++
	}
}

// comment reads a comment, if one starts here, up to its newline.
func (p *parser) comment() error {
	if p.peek() != '#' {
		return nil
	}
	for p.pos++; p.pos < len(p.src); p.pos++ {
		c := p.src[p.pos]
		if c == '\n' || (c == '\r' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '\n') {
			return nil
		}
		if c < 0x20 && c != '\t' || c == 0x7f {
			return p.fail("control character %#02x in a comment", c)
		}
	}
	return nil
}

func (p *parser) newline() bool {
	switch {
	case p.peek() == '\n':
		p.pos++
	case p.at("\r\n"):
		p.pos += 2
	default:
		return false
	}
	return true
}

// eol ends an expression: whitespace, a comment, then a newline or the end of the document.
func (p *parser) eol() error {
	p.ws()
	if err := p.comment(); err != nil {
		return err
	}
	if p.pos == len(p.src) || p.newline() {
		return nil
	}
	return p.fail("expected the end of the line, found %q", p.peek())
}

// wsCommentNewline skips what may sit between array values.
func (p *parser) wsCommentNewline() error {
	for {
		p.ws()
		if err := p.comment(); err != nil {
			return err
		}
		if !p.newline() {
			return nil
		}
	}
}

func (p *parser) header() ([]string, bool, error) {
	isAoT := p.at("[[")
	p.pos++
	if isAoT {
		p.pos++
	}
	p.ws()
	path, err := p.key()
	if err != nil {
		return nil, false, err
	}
	p.ws()
	closing := "]"
	if isAoT {
		closing = "]]"
	}
	if !p.at(closing) {
		return nil, false, p.fail("expected %s", closing)
	}
	p.pos += len(closing)
	return path, isAoT, nil
}

// openTable finds or makes the table a header names. through reports whether the path runs through an array of tables.
func (p *parser) openTable(at int, path []string, isAoT bool) (*table, bool, error) {
	t, through := p.root, false
	for _, k := range path[:len(path)-1] {
		switch x := t.vals[k].(type) {
		case nil:
			n := newTable(tImplicit)
			t.vals[k] = n
			t = n
		case *table:
			t = x
		case *aot:
			t, through = x.elems[len(x.elems)-1], true
		default:
			return nil, false, errAt(p.src, at, "%q is a value, not a table", k)
		}
	}
	last := path[len(path)-1]
	if isAoT {
		n := newTable(tHeader)
		switch x := t.vals[last].(type) {
		case nil:
			t.vals[last] = &aot{elems: []*table{n}}
		case *aot:
			x.elems = append(x.elems, n)
		default:
			return nil, false, errAt(p.src, at, "%q is already defined, not as an array of tables", strings.Join(path, "."))
		}
		return n, through, nil
	}
	switch x := t.vals[last].(type) {
	case nil:
		n := newTable(tHeader)
		t.vals[last] = n
		return n, through, nil
	case *table:
		if x.kind != tImplicit {
			return nil, false, errAt(p.src, at, "table %q is already defined", strings.Join(path, "."))
		}
		x.kind = tHeader
		return x, through, nil
	}
	return nil, false, errAt(p.src, at, "%q is already defined, not as a table", strings.Join(path, "."))
}

// define sets a dotted key's value in t.
func (p *parser) define(t *table, key []string, val any, at int) error {
	for _, k := range key[:len(key)-1] {
		switch x := t.vals[k].(type) {
		case nil:
			n := newTable(tDotted)
			t.vals[k] = n
			t = n
		case *table:
			if x.kind == tHeader {
				return errAt(p.src, at, "dotted keys can't add to table %q, defined elsewhere", k)
			}
			x.kind = tDotted
			t = x
		default:
			return errAt(p.src, at, "%q is already defined, not as a table", k)
		}
	}
	last := key[len(key)-1]
	if _, ok := t.vals[last]; ok {
		return errAt(p.src, at, "%q is already defined", strings.Join(key, "."))
	}
	t.vals[last] = val
	return nil
}

func (p *parser) key() ([]string, error) {
	var out []string
	for {
		k, err := p.simpleKey()
		if err != nil {
			return nil, err
		}
		out = append(out, k)
		save := p.pos
		p.ws()
		if p.peek() != '.' {
			p.pos = save
			return out, nil
		}
		p.pos++
		p.ws()
	}
}

func isBare(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_'
}

func (p *parser) simpleKey() (string, error) {
	switch p.peek() {
	case '"':
		if p.at(`"""`) {
			return "", p.fail("a key can't be a multi-line string")
		}
		return p.basicString()
	case '\'':
		if p.at("'''") {
			return "", p.fail("a key can't be a multi-line string")
		}
		return p.literalString()
	}
	start := p.pos
	for p.pos < len(p.src) && isBare(p.src[p.pos]) {
		p.pos++
	}
	if p.pos == start {
		return "", p.fail("expected a key")
	}
	return string(p.src[start:p.pos]), nil
}

// keyvalRest reads " = value" after a key. Members of inline tables are recorded under path when addressable.
func (p *parser) keyvalRest(path []string, addressable bool) (int, any, error) {
	p.ws()
	if p.peek() != '=' {
		return 0, nil, p.fail("expected = after a key")
	}
	p.pos++
	p.ws()
	start := p.pos
	v, err := p.value(path, addressable)
	return start, v, err
}

func (p *parser) value(path []string, addressable bool) (any, error) {
	switch c := p.peek(); c {
	case '"':
		if p.at(`"""`) {
			return p.mlString('"')
		}
		return p.basicString()
	case '\'':
		if p.at("'''") {
			return p.mlString('\'')
		}
		return p.literalString()
	case '[':
		return p.array()
	case '{':
		return p.inlineTable(path, addressable)
	case 't':
		if p.at("true") {
			p.pos += 4
			return true, nil
		}
	case 'f':
		if p.at("false") {
			p.pos += 5
			return false, nil
		}
	case 0, '\n', '\r', '#':
		return nil, p.fail("expected a value")
	}
	return p.scalar()
}

func (p *parser) array() (any, error) {
	p.pos++
	out := []any{}
	for {
		if err := p.wsCommentNewline(); err != nil {
			return nil, err
		}
		if p.peek() == ']' {
			p.pos++
			return out, nil
		}
		v, err := p.value(nil, false)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if err := p.wsCommentNewline(); err != nil {
			return nil, err
		}
		switch p.peek() {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out, nil
		default:
			return nil, p.fail("expected , or ] in an array")
		}
	}
}

func (p *parser) inlineTable(path []string, addressable bool) (any, error) {
	p.pos++
	t := newTable(tHeader)
	p.ws()
	if p.peek() == '}' {
		p.pos++
		return t.toMap(), nil
	}
	for {
		keyStart := p.pos
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		abs := append(slices.Clone(path), key...)
		valStart, val, err := p.keyvalRest(abs, addressable)
		if err != nil {
			return nil, err
		}
		if err := p.define(t, key, val, keyStart); err != nil {
			return nil, err
		}
		if addressable {
			p.keyvals = append(p.keyvals, keyval{path: abs, keyStart: keyStart, valStart: valStart, valEnd: p.pos, section: -1, member: true})
		}
		p.ws()
		switch p.peek() {
		case ',':
			p.pos++
			p.ws()
		case '}':
			p.pos++
			return t.toMap(), nil
		default:
			return nil, p.fail("expected , or } in an inline table")
		}
	}
}

func (t *table) toMap() map[string]any {
	m := make(map[string]any, len(t.vals))
	for k, v := range t.vals {
		switch x := v.(type) {
		case *table:
			m[k] = x.toMap()
		case *aot:
			a := make([]any, len(x.elems))
			for i, e := range x.elems {
				a[i] = e.toMap()
			}
			m[k] = a
		default:
			m[k] = v
		}
	}
	return m
}

func (p *parser) escape(b *strings.Builder) error {
	p.pos++ // the backslash
	c := p.peek()
	p.pos++
	switch c {
	case 'b':
		b.WriteByte('\b')
	case 't':
		b.WriteByte('\t')
	case 'n':
		b.WriteByte('\n')
	case 'f':
		b.WriteByte('\f')
	case 'r':
		b.WriteByte('\r')
	case '"':
		b.WriteByte('"')
	case '\\':
		b.WriteByte('\\')
	case 'u', 'U':
		n := 4
		if c == 'U' {
			n = 8
		}
		if p.pos+n > len(p.src) {
			return p.fail("short \\%c escape", c)
		}
		hex := string(p.src[p.pos : p.pos+n])
		r, err := strconv.ParseUint(hex, 16, 32)
		if err != nil || strings.ContainsAny(hex, "+-") || r > utf8.MaxRune || (0xd800 <= r && r <= 0xdfff) {
			return p.fail("\\%c%s isn't a Unicode scalar value", c, hex)
		}
		p.pos += n
		b.WriteRune(rune(r))
	default:
		p.pos--
		return p.fail("invalid escape \\%c", c)
	}
	return nil
}

func (p *parser) basicString() (string, error) {
	p.pos++
	var b strings.Builder
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c == '\\':
			if err := p.escape(&b); err != nil {
				return "", err
			}
		case c == '\n' || c == '\r':
			return "", p.fail("newline in a string")
		case c < 0x20 && c != '\t' || c == 0x7f:
			return "", p.fail("control character %#02x in a string", c)
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", p.fail("unterminated string")
}

func (p *parser) literalString() (string, error) {
	p.pos++
	start := p.pos
	for p.pos < len(p.src) {
		switch c := p.src[p.pos]; {
		case c == '\'':
			p.pos++
			return string(p.src[start : p.pos-1]), nil
		case c == '\n' || c == '\r':
			return "", p.fail("newline in a string")
		case c < 0x20 && c != '\t' || c == 0x7f:
			return "", p.fail("control character %#02x in a string", c)
		}
		p.pos++
	}
	return "", p.fail("unterminated string")
}

// mlString reads a multi-line basic (q is ") or literal (q is ') string.
func (p *parser) mlString(q byte) (string, error) {
	p.pos += 3
	p.newline()
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == q:
			n := 0
			for p.pos+n < len(p.src) && p.src[p.pos+n] == q {
				n++
			}
			if n < 3 {
				b.WriteString(strings.Repeat(string(q), n))
				p.pos += n
				continue
			}
			if n > 5 {
				return "", p.fail("too many quotes")
			}
			b.WriteString(strings.Repeat(string(q), n-3))
			p.pos += n
			return b.String(), nil
		case c == '\\' && q == '"':
			save := p.pos
			p.pos++
			p.ws()
			if p.newline() {
				for {
					p.ws()
					if !p.newline() {
						break
					}
				}
				continue
			}
			p.pos = save
			if err := p.escape(&b); err != nil {
				return "", err
			}
		case c == '\n':
			b.WriteByte(c)
			p.pos++
		case p.at("\r\n"):
			b.WriteString("\r\n")
			p.pos += 2
		case c < 0x20 && c != '\t' || c == 0x7f:
			return "", p.fail("control character %#02x in a string", c)
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
	return "", p.fail("unterminated string")
}

var (
	decRe   = regexp.MustCompile(`^[+-]?(?:0|[1-9](?:_?[0-9])*)$`)
	hexRe   = regexp.MustCompile(`^0x[0-9A-Fa-f](?:_?[0-9A-Fa-f])*$`)
	octRe   = regexp.MustCompile(`^0o[0-7](?:_?[0-7])*$`)
	binRe   = regexp.MustCompile(`^0b[01](?:_?[01])*$`)
	floatRe = regexp.MustCompile(`^[+-]?(?:0|[1-9](?:_?[0-9])*)(?:\.[0-9](?:_?[0-9])*)?(?:[eE][+-]?[0-9](?:_?[0-9])*)?$`)
	dateRe  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	dtRe    = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[Tt ](\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?([Zz]|[+-]\d{2}:\d{2})?$`)
	timeRe  = regexp.MustCompile(`^(\d{2}):(\d{2}):(\d{2})(?:\.(\d+))?$`)
)

func isTokenChar(c byte) bool { return isBare(c) || c == '+' || c == '.' || c == ':' }

// scalar reads a number, a date or a time.
func (p *parser) scalar() (any, error) {
	start := p.pos
	for p.pos < len(p.src) && isTokenChar(p.src[p.pos]) {
		p.pos++
	}
	// A space may separate a date from its time.
	if dateRe.Match(p.src[start:p.pos]) && p.pos+3 < len(p.src) && p.src[p.pos] == ' ' &&
		isDigit(p.src[p.pos+1]) && isDigit(p.src[p.pos+2]) && p.src[p.pos+3] == ':' {
		p.pos++
		for p.pos < len(p.src) && isTokenChar(p.src[p.pos]) {
			p.pos++
		}
	}
	tok := string(p.src[start:p.pos])
	if tok == "" {
		return nil, p.fail("expected a value")
	}
	v, err := scalarValue(tok)
	if err != nil {
		return nil, errAt(p.src, start, "%s", err.Error())
	}
	return v, nil
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

type valueError string

func (e valueError) Error() string { return string(e) }

func scalarValue(tok string) (any, error) {
	clean := strings.ReplaceAll(tok, "_", "")
	switch {
	case decRe.MatchString(tok):
		n, err := strconv.ParseInt(clean, 10, 64)
		if err != nil {
			return nil, valueError("integer " + tok + " is out of range")
		}
		return n, nil
	case hexRe.MatchString(tok), octRe.MatchString(tok), binRe.MatchString(tok):
		base := map[byte]int{'x': 16, 'o': 8, 'b': 2}[tok[1]]
		n, err := strconv.ParseInt(clean[2:], base, 64)
		if err != nil {
			return nil, valueError("integer " + tok + " is out of range")
		}
		return n, nil
	case floatRe.MatchString(tok):
		f, err := strconv.ParseFloat(clean, 64)
		if err != nil {
			return nil, valueError("float " + tok + " is out of range")
		}
		return f, nil
	}
	switch strings.TrimLeft(tok, "+-") {
	case "inf":
		if tok[0] == '-' {
			return math.Inf(-1), nil
		}
		return math.Inf(1), nil
	case "nan":
		return math.NaN(), nil
	}
	if m := dateRe.FindStringSubmatch(tok); m != nil {
		return date(m[1], m[2], m[3])
	}
	if m := timeRe.FindStringSubmatch(tok); m != nil {
		return clock(m[1], m[2], m[3], m[4])
	}
	if m := dtRe.FindStringSubmatch(tok); m != nil {
		d, err := date(m[1], m[2], m[3])
		if err != nil {
			return nil, err
		}
		t, err := clock(m[4], m[5], m[6], m[7])
		if err != nil {
			return nil, err
		}
		if m[8] == "" {
			return LocalDateTime{d, t}, nil
		}
		loc := time.UTC
		if off := m[8]; off != "Z" && off != "z" {
			h, _ := strconv.Atoi(off[1:3])
			mi, _ := strconv.Atoi(off[4:6])
			if h > 23 || mi > 59 {
				return nil, valueError("offset " + off + " is out of range")
			}
			secs := h*3600 + mi*60
			if off[0] == '-' {
				secs = -secs
			}
			loc = time.FixedZone("", secs)
		}
		return time.Date(d.Year, d.Month, d.Day, t.Hour, t.Minute, t.Second, t.Nanosecond, loc), nil
	}
	return nil, valueError("invalid value " + strconv.Quote(tok))
}

func date(y, m, d string) (LocalDate, error) {
	year, _ := strconv.Atoi(y)
	month, _ := strconv.Atoi(m)
	day, _ := strconv.Atoi(d)
	if month < 1 || month > 12 || day < 1 || day > daysIn(year, time.Month(month)) {
		return LocalDate{}, valueError("date " + y + "-" + m + "-" + d + " doesn't exist")
	}
	return LocalDate{year, time.Month(month), day}, nil
}

func daysIn(year int, m time.Month) int {
	return time.Date(year, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func clock(h, m, s, frac string) (LocalTime, error) {
	hour, _ := strconv.Atoi(h)
	minute, _ := strconv.Atoi(m)
	sec, _ := strconv.Atoi(s)
	if hour > 23 || minute > 59 || sec > 60 {
		return LocalTime{}, valueError("time " + h + ":" + m + ":" + s + " is out of range")
	}
	ns := 0
	if frac != "" {
		frac = (frac + "000000000")[:9] // extra precision is truncated, not rounded
		ns, _ = strconv.Atoi(frac)
	}
	return LocalTime{hour, minute, sec, ns}, nil
}
