package uireg

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// BufferPresenter draws a buffer as something other than its text, in one window: a markdown file as a document, a
// CSV as a table. The buffer stays open and editable, and the presentation is built again as its text changes. As
// it builds, it maps each block it draws to the source lines it came from (PresentCtx.Block); from that map the
// host puts the file's threads and decorations beside their blocks and, with Comments, lets a block be commented on.
type BufferPresenter struct {
	Name     string
	Label    string                 // what it's called: "Preview"
	When     func(path string) bool // the files it can present; nil: every file
	Comments bool                   // a click on a block, or c on the selected one, opens a thread on its lines
	Build    func(ctx *PresentCtx)
	Order    int
	Help     string
	Source   string
}

func (BufferPresenter) ContributionKind() string   { return "buffer.presenter" }
func (p BufferPresenter) ContributionName() string { return p.Name }

// PresentCtx is the buffer a presenter builds from, and the blocks it makes.
type PresentCtx struct {
	Path  string   // the file, absolute
	Name  string   // as the editor names it
	Lines []string // the buffer's text, a line each
	Width float32  // the width a block's body has

	blocks []*PresentBlock
}

// PresentBlock is one block of a presentation: the source lines it came from, [From, To] 0-based, and how it's drawn.
type PresentBlock struct {
	From, To int
	Space    float32 // above it, outside what's clicked and lit
	Label    string  // its accessible name
	Body     func(c *ui.Ctx, d PresentDraw)
}

// PresentDraw is what a block's body is drawn with: its width, and the decorations on its lines (a changed line's
// Was, for a body that shows the change).
type PresentDraw struct {
	Width       float32
	Decorations []Decoration
	Kicker      string // what the host labels the document with, for a body that draws a title: its name by default
	// Part marks a node the body drew (by its id) as a part of the block on source lines [from, to] of its own,
	// 0-based: a table's row. The host selects each part, comments on it and puts its threads beside it on its own.
	// Nil where the host takes no parts.
	Part func(from, to int, id ui.ID)
}

// Block adds a block made from source lines [from, to], 0-based.
func (c *PresentCtx) Block(from, to int, body func(c *ui.Ctx, d PresentDraw)) *PresentBlock {
	b := &PresentBlock{From: from, To: max(to, from), Body: body}
	c.blocks = append(c.blocks, b)
	return b
}

// Blocks are the blocks made, in order.
func (c *PresentCtx) Blocks() []*PresentBlock { return c.blocks }

// BlockAt is the block holding a 0-based line, or the nearest before it when no block maps it (the first, for a line
// before them all); -1 with no blocks.
func (c *PresentCtx) BlockAt(line int) int {
	at := -1
	for i, b := range c.blocks {
		if b.From <= line && line <= b.To {
			return i
		}
		if b.From <= line {
			at = i
		}
	}
	if at < 0 && len(c.blocks) > 0 {
		return 0
	}
	return at
}

// LineBlocks is BlockAt of each line in [0, n).
func (c *PresentCtx) LineBlocks(n int) []int {
	at := make([]int, n)
	last := make([]int, n) // the last block starting on each line
	for i := range at {
		at[i], last[i] = -1, -1
	}
	for i := len(c.blocks) - 1; i >= 0; i-- {
		b := c.blocks[i]
		for l := max(b.From, 0); l <= min(b.To, n-1); l++ {
			at[l] = i
		}
	}
	before := -1 // the last block starting before the line
	for i, b := range c.blocks {
		switch {
		case b.From < 0:
			before = i
		case b.From < n:
			last[b.From] = i
		}
	}
	for l := range at {
		before = max(before, last[l])
		if at[l] < 0 {
			at[l] = before
			if before < 0 && len(c.blocks) > 0 {
				at[l] = 0
			}
		}
	}
	return at
}

// Presenters are the presenters for a file, by Order then name.
func Presenters(r registry.Contributions, path string) []BufferPresenter {
	var out []BufferPresenter
	for _, p := range registry.ContributionsOf[BufferPresenter](r) {
		ok := p.When == nil
		if !ok {
			registry.Call(func() error { ok = p.When(path); return nil })
		}
		if ok {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b BufferPresenter) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// PresenterNamed is the presenter registered as name.
func PresenterNamed(r registry.Contributions, name string) (BufferPresenter, bool) {
	for _, p := range registry.ContributionsOf[BufferPresenter](r) {
		if p.Name == name {
			return p, true
		}
	}
	return BufferPresenter{}, false
}
