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
	if p.tree != 0 {
		p.m.call("tree_delete", uint64(p.tree))
	}
	p.tree = t
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
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", p.lang.Name, kind, err)
	}
	p.queries[kind] = q
	return q, nil
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
