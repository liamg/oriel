package syntax

import (
	"errors"

	"github.com/liamg/oriel/text"
)

// Match is one match of a named query: its captures, and the #set! properties of the pattern that matched.
type Match struct {
	Pattern  int // the pattern's index in the language's query
	Captures []Capture
	Props    map[string]string
}

// Capture is one captured node.
type Capture struct {
	Name       string
	Text       string
	Range      text.Range
	Start, End text.Pos // line and byte column
}

// Get is the first capture called name.
func (m Match) Get(name string) (Capture, bool) {
	for _, c := range m.Captures {
		if c.Name == name {
			return c, true
		}
	}
	return Capture{}, false
}

// All is every capture called name, in order.
func (m Match) All(name string) []Capture {
	var out []Capture
	for _, c := range m.Captures {
		if c.Name == name {
			out = append(out, c)
		}
	}
	return out
}

// Query runs the language's query of a kind ("tests", "units", or any a plugin adds) over the whole text. A
// language with no such query finds nothing.
func (p *Parser) Query(kind string) ([]Match, error) {
	q, ms, err := p.run(kind, 0, len(p.src))
	if err != nil || q == nil {
		return nil, err
	}
	out := make([]Match, 0, len(ms))
	for _, mt := range ms {
		pat := mt.q.patterns[mt.pattern]
		m := Match{Pattern: mt.q.orderOf(mt.pattern), Props: pat.set}
		for _, c := range mt.captures {
			m.Captures = append(m.Captures, Capture{
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
func (d *Document) Query(kind string) ([]Match, error) {
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
