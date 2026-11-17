// Package tree is what a parse gives back, without the parser: query matches, highlight spans, and the parse as
// other packages read it. It reaches no wasm runtime, so contracts can name it.
package tree

import "github.com/liamg/oriel/text"

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

// Span is a run of text and the highlight capture that won it, like "keyword" or "string.escape".
type Span struct {
	Range   text.Range
	Capture string
}

// Querier runs a named query ("tests", "units", or any a plugin adds) over a parse.
type Querier interface {
	Query(kind string) ([]Match, error)
}

// Doc is a buffer's parse as it's kept up to date: its queries, its highlights, and a wait for it to catch up.
type Doc interface {
	Querier
	// Highlights are the spans between byte offsets start and end.
	Highlights(start, end int) ([]Span, error)
	// Wait returns once the parse has caught up with every change it was told about.
	Wait()
}
