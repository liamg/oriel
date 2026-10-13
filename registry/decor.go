package registry

import (
	"slices"
	"strings"

	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
)

// DecorationKind is what a decoration draws.
type DecorationKind int

const (
	Sign        DecorationKind = iota // a mark in the gutter: Text in Style's colour
	LineBg                            // the line's background, and an edge in the gutter: Style
	Highlight                         // columns [From, To) of the line: Style as their colour or background
	Underline                         // a wavy underline under [From, To): Style
	VirtualText                       // Text after the end of the line, in Style
	Block                             // Node, drawn between this line and the next, as wide as the text
)

// Decoration is something drawn on, beside or between a buffer's lines. It's on Line, or at Anchor when that's
// set: an anchor the provider made on the buffer, which moves with edits, so a block stays with its text.
type Decoration struct {
	Kind     DecorationKind
	Line     int
	Anchor   text.AnchorID
	From, To int    // byte columns, for Highlight and Underline
	Style    string // a theme token: "diff.add", "error", or a syntax group "@comment"
	Edge     string // a LineBg's gutter edge, a theme token
	Text     string
	Node     ui.Node // a Block's content
	Priority int     // higher draws over lower
}

// DecorationCtx is the buffer a provider decorates and the lines in view, [Top, Bottom).
type DecorationCtx struct {
	Name        string // the buffer's file, as the editor names it
	Path        string // the buffer's file, absolute
	Buffer      *text.Buffer
	Snap        text.Snapshot
	Top, Bottom int
}

// DecorationProvider gives the decorations of the lines in view; it's called each frame they're drawn.
type DecorationProvider interface {
	Decorations(ctx DecorationCtx) []Decoration
}

// DecorationFunc is a func as a DecorationProvider.
type DecorationFunc func(ctx DecorationCtx) []Decoration

func (f DecorationFunc) Decorations(ctx DecorationCtx) []Decoration { return f(ctx) }

// NamedProvider is a provider with the id it was registered under.
type NamedProvider struct {
	ID string
	DecorationProvider
}

func (r *reg) Decorations(id string, p DecorationProvider) Disposable {
	r.mu.Lock()
	defer r.mu.Unlock()
	tok := r.decor.push(id, p)
	return disposeOnce(func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.decor.remove(id, tok)
	})
}

func (r *reg) DecorationProviders() []NamedProvider {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]NamedProvider, 0, len(r.decor))
	for id := range r.decor {
		p, _ := r.decor.top(id)
		out = append(out, NamedProvider{id, p})
	}
	slices.SortFunc(out, func(a, b NamedProvider) int { return strings.Compare(a.ID, b.ID) })
	return out
}
