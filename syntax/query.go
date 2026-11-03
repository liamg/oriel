package syntax

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// query is a compiled tree-sitter query in one module, with the predicates and directives the host runs.
type query struct {
	ptr      uint32
	captures []string
	patterns []pattern
	want     uint32 // in the module: a byte per capture, set for those #has-parent? reads, which alone get a parent
	root     *query // patterns on the root node that run without the window (splitRootPatterns)
	order    []int  // each pattern's index in the language's query, when it's been split
	off      bool   // every pattern is turned off: running it would find nothing
}

// orderOf is pattern i's place in the language's query: later ones win.
func (q *query) orderOf(i int) int {
	if i < len(q.order) {
		return q.order[i]
	}
	return i
}

type pattern struct {
	preds []predicate
	set   map[string]string // #set! key value
	// offsets move a capture's range: #offset! @c start-row start-col end-row end-col.
	offsets map[int][4]int
}

// predicate is one #name? or #name! with its arguments: capture indexes or strings.
type predicate struct {
	name string
	args []arg
	re   *regexp.Regexp
	lua  *luaPattern
}

type arg struct {
	capture int // -1 for a string
	str     string
}

var queryErrors = []string{"none", "syntax", "node type", "field", "capture", "structure", "language"}

func compileQuery(m *module, src string) (*query, error) {
	sp := m.put([]byte(src))
	defer m.free(sp)
	errp := m.scratch(2)
	defer m.free(errp)
	ptr := m.call("query_new", uint64(sp), uint64(len(src)), uint64(errp))
	if ptr == 0 {
		off, typ := m.u32(errp), m.u32(errp+4)
		kind := "unknown"
		if int(typ) < len(queryErrors) {
			kind = queryErrors[typ]
		}
		line := strings.Count(src[:min(int(off), len(src))], "\n") + 1
		return nil, fmt.Errorf("syntax: query %s error at line %d", kind, line)
	}
	q := &query{ptr: ptr}
	for i := range m.call("query_capture_count", uint64(ptr)) {
		q.captures = append(q.captures, m.str("query_capture_name", uint64(ptr), uint64(i)))
	}
	var strs []string
	for i := range m.call("query_string_count", uint64(ptr)) {
		strs = append(strs, m.str("query_string", uint64(ptr), uint64(i)))
	}
	for i := range m.call("query_pattern_count", uint64(ptr)) {
		lp := m.scratch(1)
		sp := m.call("query_predicates", uint64(ptr), uint64(i), uint64(lp))
		n := m.u32(lp)
		m.free(lp)
		steps := m.u32s(sp, n*2)
		p, err := parsePredicates(steps, strs)
		if err != nil {
			return nil, err
		}
		q.patterns = append(q.patterns, p)
	}
	want := make([]byte, max(1, len(q.captures)))
	for _, p := range q.patterns {
		for _, pr := range p.preds {
			if strings.TrimPrefix(pr.name, "not-") == "has-parent?" && len(pr.args) > 0 && pr.args[0].capture >= 0 {
				want[pr.args[0].capture] = 1
			}
		}
	}
	q.want = m.put(want)
	return q, nil
}

// parsePredicates turns tree-sitter's predicate steps (type, value: 1 capture, 2 string, 0 end) into
// predicates and directives.
func parsePredicates(steps []uint32, strs []string) (pattern, error) {
	var p pattern
	var cur []arg
	for i := 0; i+1 < len(steps); i += 2 {
		typ, val := steps[i], steps[i+1]
		switch typ {
		case 1:
			cur = append(cur, arg{capture: int(val)})
		case 2:
			cur = append(cur, arg{capture: -1, str: strs[val]})
		case 0:
			if len(cur) == 0 || cur[0].capture >= 0 {
				return p, fmt.Errorf("syntax: predicate without a name")
			}
			name, args := cur[0].str, cur[1:]
			cur = nil
			switch name {
			case "set!":
				if p.set == nil {
					p.set = map[string]string{}
				}
				// (#set! key value) or (#set! @capture key value): keyed the same either way.
				var kv []string
				for _, a := range args {
					if a.capture < 0 {
						kv = append(kv, a.str)
					}
				}
				if len(kv) == 1 {
					p.set[kv[0]] = ""
				} else if len(kv) >= 2 {
					p.set[kv[0]] = kv[1]
				}
			case "offset!":
				if len(args) == 5 && args[0].capture >= 0 {
					var o [4]int
					for j := range 4 {
						o[j], _ = strconv.Atoi(args[j+1].str)
					}
					if p.offsets == nil {
						p.offsets = map[int][4]int{}
					}
					p.offsets[args[0].capture] = o
				}
			default:
				pr := predicate{name: name, args: args}
				base := strings.TrimPrefix(strings.TrimPrefix(name, "not-"), "any-")
				if (base == "match?" || base == "vim-match?") && len(args) == 2 {
					re, err := regexp.Compile(args[1].str)
					if err != nil {
						// vim regex Go can't read: the predicate never matches.
						re = nil
					}
					pr.re = re
				}
				if base == "lua-match?" && len(args) == 2 {
					pr.lua = compileLuaPattern(args[1].str)
				}
				p.preds = append(p.preds, pr)
			}
		}
	}
	return p, nil
}

// capture is one captured node of a match.
type capture struct {
	index            int
	start, end       int
	startRow, startC int
	endRow, endC     int
	parent           uint32
}

type match struct {
	q        *query // the query it's from, for its patterns and capture names
	pattern  int
	captures []capture
}

// matches runs q over [start, end) of tree t and returns the matches whose predicates hold.
func (q *query) matches(m *module, tree uint32, start, end int, src []byte, symbol func(string) uint32) []match {
	out := q.run(m, tree, start, end, src, symbol, true)
	if q.root != nil {
		out = append(out, q.root.run(m, tree, start, end, src, symbol, false)...)
	}
	return out
}

func (q *query) run(m *module, tree uint32, start, end int, src []byte, symbol func(string) uint32, windowed bool) []match {
	if q.off {
		return nil
	}
	win := 0
	if windowed {
		win = 1
	}
	lp := m.scratch(1)
	p := m.call("query_matches", uint64(q.ptr), uint64(tree), uint64(start), uint64(end), uint64(lp), uint64(q.want), uint64(win))
	n := m.u32(lp)
	m.free(lp)
	raw := m.u32s(p, n)
	var out []match
	for i := 0; i < len(raw); {
		mt := match{q: q, pattern: int(raw[i])}
		count := int(raw[i+1])
		i += 2
		for range count {
			c := raw[i : i+8]
			mt.captures = append(mt.captures, capture{int(c[0]), int(c[1]), int(c[2]), int(c[3]), int(c[4]), int(c[5]), int(c[6]), c[7]})
			i += 8
		}
		if q.holds(mt, src, symbol) {
			out = append(out, mt)
		}
	}
	return out
}

func (q *query) holds(mt match, src []byte, symbol func(string) uint32) bool {
	text := func(a arg) []string {
		if a.capture < 0 {
			return []string{a.str}
		}
		var out []string
		for _, c := range mt.captures {
			if c.index == a.capture {
				out = append(out, string(src[c.start:c.end]))
			}
		}
		return out
	}
	nodes := func(a arg) []capture {
		var out []capture
		for _, c := range mt.captures {
			if c.index == a.capture {
				out = append(out, c)
			}
		}
		return out
	}
	for _, p := range q.patterns[mt.pattern].preds {
		neg := strings.HasPrefix(p.name, "not-")
		name := strings.TrimPrefix(p.name, "not-")
		if len(p.args) == 0 {
			continue
		}
		subjects := text(p.args[0])
		if len(subjects) == 0 {
			continue
		}
		var ok bool
		switch name {
		case "eq?":
			ok = len(p.args) == 2 && allOf(subjects, func(s string) bool { return contains(text(p.args[1]), s) })
		case "any-eq?":
			ok = len(p.args) == 2 && anyOf(subjects, func(s string) bool { return contains(text(p.args[1]), s) })
		case "any-of?":
			var set []string
			for _, a := range p.args[1:] {
				set = append(set, a.str)
			}
			ok = allOf(subjects, func(s string) bool { return contains(set, s) })
		case "match?", "vim-match?", "any-match?":
			ok = p.re != nil && allOf(subjects, func(s string) bool { return p.re.MatchString(s) })
		case "lua-match?", "any-lua-match?":
			ok = p.lua != nil && allOf(subjects, func(s string) bool { return p.lua.find(s) })
		case "contains?", "any-contains?":
			ok = anyOf(subjects, func(s string) bool {
				for _, a := range p.args[1:] {
					if strings.Contains(s, a.str) {
						return true
					}
				}
				return false
			})
		case "has-parent?":
			ok = false
			for _, c := range nodes(p.args[0]) {
				for _, a := range p.args[1:] {
					if sym := symbol(a.str); sym != 0 && c.parent == sym {
						ok = true
					}
				}
			}
		default:
			// Predicates we don't know (#has-ancestor? and the like) don't filter.
			continue
		}
		if ok == neg {
			return false
		}
	}
	return true
}

func contains(set []string, s string) bool {
	for _, x := range set {
		if x == s {
			return true
		}
	}
	return false
}

func allOf(ss []string, f func(string) bool) bool {
	for _, s := range ss {
		if !f(s) {
			return false
		}
	}
	return true
}

func anyOf(ss []string, f func(string) bool) bool {
	for _, s := range ss {
		if f(s) {
			return true
		}
	}
	return false
}
