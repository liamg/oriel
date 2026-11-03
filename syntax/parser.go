package syntax

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"github.com/liamg/oriel/text"
)

// Parser keeps one buffer's syntax tree, and updates it incrementally as the buffer changes.
// It isn't safe for concurrent use.
type Parser struct {
	lang    *Language
	m       *module
	parser  uint32
	tree    uint32
	src     []byte
	version uint64 // the buffer version src is, so Update knows the change follows on
	synced  bool
	queries map[string]*query
	symbols map[string]uint32
	// injected holds a parser per language other languages' text is injected as (code in Markdown).
	injected map[string]*Parser
	depth    int
	queried  int    // bytes run through the highlights query, for tests of how much an edit re-highlights
	prev     uint32 // the tree before the last reparse, edited, for Changed
	whole    bool   // the last parse was from scratch
}

// NewParser starts a parser for a language.
func NewParser(l *Language) (*Parser, error) {
	m, err := instantiate(l)
	if err != nil {
		return nil, err
	}
	return &Parser{lang: l, m: m, parser: m.call("parser_new"), queries: map[string]*query{}, symbols: map[string]uint32{}}, nil
}

// Close frees the parser and everything it holds.
func (p *Parser) Close() {
	for _, ip := range p.injected {
		ip.Close()
	}
	p.m.close()
}

// Language is the parser's language.
func (p *Parser) Language() *Language { return p.lang }

// Parse parses text from scratch.
func (p *Parser) Parse(src []byte) {
	p.synced = false
	if p.tree != 0 {
		p.m.call("tree_delete", uint64(p.tree))
		p.tree = 0
	}
	p.src = append(p.src[:0], src...)
	p.reparse()
}

func (p *Parser) reparse() {
	sp := p.m.put(p.src)
	t := p.m.call("parse", uint64(p.parser), uint64(p.tree), uint64(sp), uint64(len(p.src)))
	p.m.free(sp)
	if p.prev != 0 {
		p.m.call("tree_delete", uint64(p.prev))
	}
	p.prev, p.tree, p.whole = p.tree, t, false
}

// Changed is where the last reparse's syntax differs from the tree before it, worked out when asked: all of the
// text after a parse from scratch.
func (p *Parser) Changed() []text.Range {
	if p.whole || p.prev == 0 {
		return []text.Range{{Start: 0, End: len(p.src)}}
	}
	lp := p.m.scratch(1)
	r := p.m.u32s(p.m.call("changed_ranges", uint64(p.prev), uint64(p.tree), uint64(lp)), p.m.u32(lp))
	p.m.free(lp)
	var out []text.Range
	for i := 0; i+1 < len(r); i += 2 {
		out = append(out, text.Range{Start: int(r[i]), End: int(r[i+1])})
	}
	return out
}

// ParseSnapshot parses a buffer snapshot from scratch, and remembers its version for Update.
func (p *Parser) ParseSnapshot(s text.Snapshot) {
	p.Parse(s.Bytes())
	p.version, p.synced = s.Version(), true
}

// Update brings the tree up to date with a change to the buffer it was parsed from: the old tree is
// edited and reused, so only what changed is parsed again. A change that doesn't follow on from the last
// one the parser saw is parsed from scratch.
func (p *Parser) Update(ch text.Change) {
	if p.tree == 0 || !p.synced || ch.Before.Version() != p.version {
		p.ParseSnapshot(ch.After)
		p.whole = true
		return
	}
	single := len(ch.Replaces) == 1
	for _, r := range ch.Replaces {
		start, oldEnd := r.Range.Start, r.Range.End
		newEnd := start + len(r.Text)
		var sp, op, np [2]uint32
		if single {
			// One replace: the snapshots give the points without scanning the text.
			sp, op, np = snapPoint(ch.Before, start), snapPoint(ch.Before, oldEnd), snapPoint(ch.After, newEnd)
		} else {
			sp, op = point(p.src, start), point(p.src, oldEnd)
			p.src = append(p.src[:start:start], append(append([]byte(nil), r.Text...), p.src[oldEnd:]...)...)
			np = point(p.src, newEnd)
		}
		e := []uint32{uint32(start), uint32(oldEnd), uint32(newEnd), sp[0], sp[1], op[0], op[1], np[0], np[1]}
		ep := p.m.put(u32bytes(e))
		p.m.call("tree_edit", uint64(p.tree), uint64(ep))
		p.m.free(ep)
	}
	if single {
		p.src = ch.After.Bytes()
	}
	p.version = ch.After.Version()
	p.reparse()
}

func snapPoint(s text.Snapshot, off int) [2]uint32 {
	pos := s.PosAt(off)
	return [2]uint32{uint32(pos.Line), uint32(pos.Col)}
}

// point is the row and byte column of an offset, as tree-sitter counts them.
func point(src []byte, off int) [2]uint32 {
	row := bytes.Count(src[:off], []byte("\n"))
	col := off - (bytes.LastIndexByte(src[:off], '\n') + 1)
	return [2]uint32{uint32(row), uint32(col)}
}

func u32bytes(v []uint32) []byte {
	b := make([]byte, len(v)*4)
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[i*4:], x)
	}
	return b
}

// HasError reports whether the text has syntax errors.
func (p *Parser) HasError() bool {
	return p.tree != 0 && p.m.call("tree_has_error", uint64(p.tree)) != 0
}

func (p *Parser) query(kind string) (*query, error) {
	if q, ok := p.queries[kind]; ok {
		return q, nil
	}
	src := p.lang.query(kind)
	if src == "" {
		p.queries[kind] = nil
		return nil, nil
	}
	q, err := compileQuery(p.m, src)
	if err == nil && p.tree != 0 {
		q, err = p.splitRoot(q, src)
	}
	if err == nil && kind == "injections" {
		p.dropMissingInjections(q)
	}
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", p.lang.Name, kind, err)
	}
	p.queries[kind] = q
	return q, nil
}

// splitRoot recompiles q without its patterns on the root node, which run apart (splitRootPatterns); q as it
// is when it has none.
func (p *Parser) splitRoot(q *query, src string) (*query, error) {
	root := p.m.str("root_type", uint64(p.tree))
	var pats []string
	found := false
	sp := p.m.scratch(2)
	defer p.m.free(sp)
	for i := range q.patterns {
		p.m.call("query_pattern_span", uint64(q.ptr), uint64(i), uint64(sp))
		pat := src[p.m.u32(sp):p.m.u32(sp+4)]
		pats = append(pats, pat)
		found = found || mentions(pat, root)
	}
	if !found {
		return q, nil
	}
	p.m.call("query_delete", uint64(q.ptr))
	main, rest, mainOrder, restOrder := splitRootPatterns(pats, root)
	q, err := compileQuery(p.m, main)
	if err != nil {
		return nil, err
	}
	q.order = mainOrder
	if len(restOrder) > 0 {
		if q.root, err = compileQuery(p.m, rest); err != nil {
			return nil, err
		}
		q.root.order = restOrder
	}
	return q, nil
}

// dropMissingInjections turns off injection patterns into a language the set doesn't have, which would only be
// matched to be thrown away.
func (p *Parser) dropMissingInjections(q *query) {
	for _, qq := range []*query{q, q.root} {
		if qq == nil {
			continue
		}
		off := 0
		for i, pat := range qq.patterns {
			if lang := pat.set["injection.language"]; lang != "" {
				if _, ok := p.lang.lookup(lang); !ok {
					p.m.call("query_disable_pattern", uint64(qq.ptr), uint64(i))
					off++
				}
			}
		}
		qq.off = off == len(qq.patterns)
	}
}

func (p *Parser) symbol(name string) uint32 {
	if s, ok := p.symbols[name]; ok {
		return s
	}
	np := p.m.put([]byte(name))
	s := p.m.call("symbol_for_name", uint64(np), uint64(len(name)))
	p.m.free(np)
	p.symbols[name] = s
	return s
}

// run runs a query of a kind over [start, end).
func (p *Parser) run(kind string, start, end int) (*query, []match, error) {
	q, err := p.query(kind)
	if err != nil || q == nil || p.tree == 0 {
		return q, nil, err
	}
	return q, q.matches(p.m, p.tree, start, end, p.src, p.symbol), nil
}

// parseRanges parses the parser's text limited to ranges, for an injection.
func (p *Parser) parseRanges(src []byte, ranges [][2]int) {
	p.src = src
	var r []uint32
	for _, x := range ranges {
		s, e := point(src, x[0]), point(src, x[1])
		r = append(r, uint32(x[0]), uint32(x[1]), s[0], s[1], e[0], e[1])
	}
	rp := p.m.put(u32bytes(r))
	p.m.call("set_ranges", uint64(p.parser), uint64(rp), uint64(len(ranges)))
	p.m.free(rp)
	if p.tree != 0 {
		p.m.call("tree_delete", uint64(p.tree))
		p.tree = 0
	}
	p.reparse()
}

// NodePath is the kinds of the named nodes from the root down to the smallest at byte off, for :Inspect.
func (p *Parser) NodePath(off int) []string {
	if p.tree == 0 {
		return nil
	}
	lenp := p.m.scratch(1)
	defer p.m.free(lenp)
	at := p.m.call("node_path", uint64(p.tree), uint64(off), uint64(lenp))
	b := p.m.bytes(at, p.m.u32(lenp))
	var out []string
	for _, k := range bytes.Split(bytes.TrimSuffix(b, []byte{0}), []byte{0}) {
		out = append(out, string(k))
	}
	return out
}
