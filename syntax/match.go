package syntax

import (
	"errors"

	"github.com/liamg/oriel/syntax/tree"
	"github.com/liamg/oriel/text"
)

// Query runs the language's query of a kind ("tests", "units", or any a plugin adds) over the whole text. A
// language with no such query finds nothing.
func (p *Parser) Query(kind string) ([]tree.Match, error) {
	q, ms, err := p.run(kind, 0, len(p.src))
	if err != nil || q == nil {
		return nil, err
	}
	out := make([]tree.Match, 0, len(ms))
	for _, mt := range ms {
		pat := mt.q.patterns[mt.pattern]
		m := tree.Match{Pattern: mt.q.orderOf(mt.pattern), Props: pat.set}
		for _, c := range mt.captures {
			m.Captures = append(m.Captures, tree.Capture{
				Name:  mt.q.captures[c.index],
				Text:  string(p.src[c.start:c.end]),
				Range: text.Range{Start: c.start, End: c.end},
				Start: text.Pos{Line: c.startRow, Col: c.startC},
				End:   text.Pos{Line: c.endRow, Col: c.endC},
			})
		}
		out = append(out, m)
	}
	return out, nil
}

// Query runs a named query over the latest snapshot the document was told about, once it's parsed.
func (d *Document) Query(kind string) ([]tree.Match, error) {
	d.mu.Lock()
	for (len(d.pending) > 0 || d.busy || d.querying) && !d.closed {
		d.cond.Wait()
	}
	if d.closed {
		d.mu.Unlock()
		return nil, errClosed
	}
	d.querying = true
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.querying = false
		d.mu.Unlock()
		d.cond.Broadcast()
	}()
	return d.p.Query(kind)
}

var errClosed = errors.New("syntax: document is closed")
