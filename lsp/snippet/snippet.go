// Package snippet expands LSP snippets (the TextMate-style syntax completion items use) into plain text and the
// tab stops in it.
package snippet

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/text"
)

// Range is a span of byte offsets in a Snippet's Text.
type Range = text.Range

// Tabstop is one tab stop: every place it appears, and the choices it offers.
type Tabstop struct {
	Index   int
	Ranges  []Range
	Choices []string
}

// Snippet is a snippet expanded: its text and tab stops, in the order they're visited ($0 last).
type Snippet struct {
	Text     string
	Tabstops []Tabstop
}

// Vars resolves a variable: its value, and whether the name is known at all.
type Vars func(name string) (value string, known bool)

// Plain is s as plain text with no tab stops.
func Plain(s string) Snippet { return Snippet{Text: s} }

// Parse expands s. Anything that isn't valid snippet syntax is kept as text.
func Parse(s string, vars Vars) Snippet {
	p := &parser{s: s}
	nodes := p.any(false)
	r := &renderer{vars: vars, holders: map[int]*node{}}
	r.prepare(nodes)
	r.render(nodes, map[int]bool{})
	return r.result()
}

type kind int

const (
	textNode kind = iota
	tabNode
	varNode
)

type node struct {
	kind     kind
	text     string // text, or a variable's name
	index    int
	children []*node // a placeholder's or a variable's default
	holder   bool    // has a placeholder (or default)
	choices  []string
	xform    *transform
}

type transform struct {
	re     *regexp.Regexp
	global bool
	format []formatPart
}

type formatPart struct {
	text      string
	group     int // -1 for text
	op        string
	ifText    string
	elseText  string
	hasIfElse bool
}

type parser struct {
	s string
	i int
}

func (p *parser) peek(prefix string) bool { return strings.HasPrefix(p.s[p.i:], prefix) }

// any parses until the end, or an unescaped '}' when nested.
func (p *parser) any(nested bool) []*node {
	var out []*node
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, &node{kind: textNode, text: b.String()})
			b.Reset()
		}
	}
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.s) && strings.IndexByte(`$}\`, p.s[p.i+1]) >= 0:
			b.WriteByte(p.s[p.i+1])
			p.i += 2
		case c == '}' && nested:
			flush()
			return out
		case c == '$':
			start := p.i
			if n := p.dollar(); n != nil {
				flush()
				out = append(out, n)
			} else {
				p.i = start + 1
				b.WriteByte('$')
			}
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	flush()
	return out
}

func (p *parser) int() (int, bool) {
	j := p.i
	for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
		j++
	}
	if j == p.i {
		return 0, false
	}
	n, err := strconv.Atoi(p.s[p.i:j])
	p.i = j
	return n, err == nil
}

func (p *parser) name() (string, bool) {
	j := p.i
	for j < len(p.s) {
		c := p.s[j]
		if c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || j > p.i && c >= '0' && c <= '9' {
			j++
			continue
		}
		break
	}
	if j == p.i {
		return "", false
	}
	n := p.s[p.i:j]
	p.i = j
	return n, true
}

// dollar parses what follows a '$', or returns nil to keep the '$' as text.
func (p *parser) dollar() *node {
	p.i++
	if n, ok := p.int(); ok {
		return &node{kind: tabNode, index: n}
	}
	if name, ok := p.name(); ok {
		return &node{kind: varNode, text: name}
	}
	if !p.peek("{") {
		return nil
	}
	p.i++
	if n, ok := p.int(); ok {
		switch {
		case p.peek("}"):
			p.i++
			return &node{kind: tabNode, index: n}
		case p.peek(":"):
			p.i++
			children := p.any(true)
			if !p.peek("}") {
				return nil
			}
			p.i++
			return &node{kind: tabNode, index: n, children: children, holder: true}
		case p.peek("|"):
			p.i++
			choices, ok := p.choices()
			if !ok {
				return nil
			}
			return &node{kind: tabNode, index: n, choices: choices, holder: true,
				children: []*node{{kind: textNode, text: choices[0]}}}
		}
		return nil
	}
	name, ok := p.name()
	if !ok {
		return nil
	}
	switch {
	case p.peek("}"):
		p.i++
		return &node{kind: varNode, text: name}
	case p.peek(":"):
		p.i++
		children := p.any(true)
		if !p.peek("}") {
			return nil
		}
		p.i++
		return &node{kind: varNode, text: name, children: children, holder: true}
	case p.peek("/"):
		p.i++
		x, ok := p.transform()
		if !ok {
			return nil
		}
		return &node{kind: varNode, text: name, xform: x}
	}
	return nil
}

func (p *parser) choices() ([]string, bool) {
	var out []string
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		switch {
		case c == '\\' && p.i+1 < len(p.s) && strings.IndexByte(`$}\,|`, p.s[p.i+1]) >= 0:
			b.WriteByte(p.s[p.i+1])
			p.i += 2
		case c == ',':
			out = append(out, b.String())
			b.Reset()
			p.i++
		case c == '|':
			if !p.peek("|}") {
				return nil, false
			}
			p.i += 2
			return append(out, b.String()), true
		default:
			b.WriteByte(c)
			p.i++
		}
	}
	return nil, false
}

// until reads to an unescaped stop byte; `\stop` becomes stop and other escapes are kept for the regexp.
func (p *parser) until(stop byte) (string, bool) {
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		if c == '\\' && p.i+1 < len(p.s) {
			if p.s[p.i+1] == stop {
				b.WriteByte(stop)
			} else {
				b.WriteString(p.s[p.i : p.i+2])
			}
			p.i += 2
			continue
		}
		if c == stop {
			p.i++
			return b.String(), true
		}
		b.WriteByte(c)
		p.i++
	}
	return "", false
}

// format reads a transform's format to its closing '/', which can't be inside a ${…}.
func (p *parser) format() (string, bool) {
	start, depth := p.i, 0
	for p.i < len(p.s) {
		switch c := p.s[p.i]; {
		case c == '\\':
			p.i++
		case c == '$' && p.peek("${"):
			depth++
			p.i++
		case c == '}' && depth > 0:
			depth--
		case c == '/' && depth == 0:
			p.i++
			return p.s[start : p.i-1], true
		}
		p.i++
	}
	return "", false
}

func (p *parser) transform() (*transform, bool) {
	re, ok := p.until('/')
	if !ok {
		return nil, false
	}
	format, ok := p.format()
	if !ok {
		return nil, false
	}
	opts, ok := p.until('}')
	if !ok {
		return nil, false
	}
	flags := ""
	x := &transform{}
	for _, o := range opts {
		switch o {
		case 'g':
			x.global = true
		case 'i', 'm', 's':
			flags += string(o)
		}
	}
	if flags != "" {
		re = "(?" + flags + ")" + re
	}
	var err error
	if x.re, err = regexp.Compile(re); err != nil {
		return nil, false
	}
	x.format = parseFormat(format)
	return x, true
}

func parseFormat(s string) []formatPart {
	var out []formatPart
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, formatPart{text: b.String(), group: -1})
			b.Reset()
		}
	}
	p := &parser{s: s}
	for p.i < len(s) {
		c := s[p.i]
		if c == '\\' && p.i+1 < len(s) {
			b.WriteByte(s[p.i+1])
			p.i += 2
			continue
		}
		if c != '$' {
			b.WriteByte(c)
			p.i++
			continue
		}
		start := p.i
		if f, ok := p.formatRef(); ok {
			flush()
			out = append(out, f)
			continue
		}
		p.i = start + 1
		b.WriteByte('$')
	}
	flush()
	return out
}

func (p *parser) formatRef() (formatPart, bool) {
	p.i++
	if n, ok := p.int(); ok {
		return formatPart{group: n}, true
	}
	if !p.peek("{") {
		return formatPart{}, false
	}
	p.i++
	n, ok := p.int()
	if !ok {
		return formatPart{}, false
	}
	f := formatPart{group: n}
	if p.peek("}") {
		p.i++
		return f, true
	}
	if !p.peek(":") {
		return formatPart{}, false
	}
	p.i++
	switch {
	case p.peek("/"):
		p.i++
		op, ok := p.until('}')
		f.op = op
		return f, ok
	case p.peek("+"):
		p.i++
		f.ifText, ok = p.until('}')
		f.op = "+"
		return f, ok
	case p.peek("?"):
		p.i++
		if f.ifText, ok = p.until(':'); !ok {
			return f, false
		}
		f.elseText, ok = p.until('}')
		f.op = "?"
		return f, ok
	case p.peek("-"):
		p.i++
	}
	f.elseText, ok = p.until('}')
	f.op = "-"
	return f, ok
}

func (x *transform) apply(v string) string {
	matches := x.re.FindAllStringSubmatchIndex(v, -1)
	if !x.global && len(matches) > 1 {
		matches = matches[:1]
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(v[last:m[0]])
		for _, f := range x.format {
			if f.group < 0 {
				b.WriteString(f.text)
				continue
			}
			g, ok := "", false
			if 2*f.group+1 < len(m) && m[2*f.group] >= 0 {
				g, ok = v[m[2*f.group]:m[2*f.group+1]], true
			}
			ok = ok && g != ""
			switch f.op {
			case "":
				b.WriteString(g)
			case "upcase":
				b.WriteString(strings.ToUpper(g))
			case "downcase":
				b.WriteString(strings.ToLower(g))
			case "capitalize":
				if r, n := utf8.DecodeRuneInString(g); n > 0 {
					b.WriteString(string(unicode.ToUpper(r)) + g[n:])
				}
			case "+":
				if ok {
					b.WriteString(f.ifText)
				}
			case "?":
				if ok {
					b.WriteString(f.ifText)
				} else {
					b.WriteString(f.elseText)
				}
			case "-":
				if ok {
					b.WriteString(g)
				} else {
					b.WriteString(f.elseText)
				}
			}
		}
		last = m[1]
	}
	b.WriteString(v[last:])
	return b.String()
}

type renderer struct {
	vars    Vars
	b       strings.Builder
	holders map[int]*node // the first placeholder of each index
	stops   map[int]*Tabstop
}

// prepare finds each index's placeholder and turns unknown variables into placeholders after the last index.
func (r *renderer) prepare(nodes []*node) {
	maxIndex := 0
	var unknown []*node
	var walk func([]*node)
	walk = func(ns []*node) {
		for _, n := range ns {
			switch n.kind {
			case tabNode:
				maxIndex = max(maxIndex, n.index)
				if n.holder && r.holders[n.index] == nil {
					r.holders[n.index] = n
				}
			case varNode:
				if _, known := r.lookup(n.text); !known && n.xform == nil {
					unknown = append(unknown, n)
				}
			}
			walk(n.children)
		}
	}
	walk(nodes)
	for _, n := range unknown {
		maxIndex++
		*n = node{kind: tabNode, index: maxIndex, holder: true, children: []*node{{kind: textNode, text: n.text}}}
		r.holders[n.index] = n
	}
}

func (r *renderer) lookup(name string) (string, bool) {
	if r.vars == nil {
		return "", false
	}
	return r.vars(name)
}

func (r *renderer) render(nodes []*node, active map[int]bool) {
	for _, n := range nodes {
		switch n.kind {
		case textNode:
			r.b.WriteString(n.text)
		case varNode:
			v, _ := r.lookup(n.text)
			switch {
			case n.xform != nil:
				r.b.WriteString(n.xform.apply(v))
			case v == "" && n.holder:
				r.render(n.children, active)
			default:
				r.b.WriteString(v)
			}
		case tabNode:
			start := r.b.Len()
			if h := r.holders[n.index]; h != nil && !active[n.index] {
				active[n.index] = true
				r.render(h.children, active)
				delete(active, n.index)
			}
			r.stop(n.index, Range{Start: start, End: r.b.Len()}, n.choices)
		}
	}
}

func (r *renderer) stop(i int, at Range, choices []string) {
	if r.stops == nil {
		r.stops = map[int]*Tabstop{}
	}
	s := r.stops[i]
	if s == nil {
		s = &Tabstop{Index: i}
		r.stops[i] = s
	}
	s.Ranges = append(s.Ranges, at)
	if s.Choices == nil {
		s.Choices = choices
	}
}

func (r *renderer) result() Snippet {
	out := Snippet{Text: r.b.String()}
	for _, s := range r.stops {
		out.Tabstops = append(out.Tabstops, *s)
	}
	slices.SortFunc(out.Tabstops, func(a, b Tabstop) int {
		ai, bi := a.Index, b.Index
		if ai == 0 {
			ai = int(^uint(0) >> 1)
		}
		if bi == 0 {
			bi = int(^uint(0) >> 1)
		}
		return ai - bi
	})
	return out
}
