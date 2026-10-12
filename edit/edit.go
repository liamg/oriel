// Package edit holds the editing primitives every input engine drives: cursors, edits that go through
// undo and authorship, registers, marks and messages. Engines never touch text.Buffer directly.
package edit

import (
	"slices"
	"sync"

	"github.com/liamg/oriel/text"
)

// RegKind is how a register's text goes back in.
type RegKind int

const (
	Charwise RegKind = iota
	Linewise
	Blockwise
)

// Register is a named piece of text an engine yanks to and puts from.
type Register struct {
	Text string
	Kind RegKind
}

// Cursor is a cursor's offset in the buffer.
type Cursor struct {
	Off int
}

// Editor is one window's view of a buffer: its cursors, plus the registers and messages it shares
// with whatever else edits through it.
type Editor struct {
	buf    *text.Buffer
	author text.Author

	mu      sync.Mutex
	cursors []Cursor
	regs    map[rune]Register
	msgs    []string
}

// New returns an editor over buf whose edits are by author.
func New(buf *text.Buffer, author text.Author) *Editor {
	return &Editor{buf: buf, author: author, cursors: []Cursor{{}}, regs: map[rune]Register{}}
}

func (e *Editor) Buf() text.Snapshot { return e.buf.Snapshot() }

// Cursors returns the cursors, primary first.
func (e *Editor) Cursors() []Cursor {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.cursors)
}

func (e *Editor) SetCursors(cs []Cursor) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cursors = slices.Clone(cs)
}

// Do applies replaces, in order and not overlapping, as one undo step, or as part of g's step.
func (e *Editor) Do(rs []text.Replace, g text.UndoGroup) (text.Change, error) {
	return e.buf.Apply(text.Edit{Ranges: rs, Author: e.author, Group: g})
}

func (e *Editor) NewGroup() text.UndoGroup                       { return e.buf.NewGroup() }
func (e *Editor) Undo() (text.Change, bool)                      { return e.buf.Undo() }
func (e *Editor) Redo() (text.Change, bool)                      { return e.buf.Redo() }
func (e *Editor) UndoSeq() int                                   { return e.buf.UndoSeq() }
func (e *Editor) UndoParent(seq int) (int, bool)                 { return e.buf.UndoParent(seq) }
func (e *Editor) UndoTo(seq int) (text.Change, error)            { return e.buf.UndoTo(seq) }
func (e *Editor) Anchor(off int, b text.Bias) text.AnchorID      { return e.buf.Anchor(off, b) }
func (e *Editor) Resolve(id text.AnchorID) (int, bool)           { return e.buf.Resolve(id) }
func (e *Editor) Release(id text.AnchorID)                       { e.buf.Release(id) }
func (e *Editor) Subscribe(fn func(text.Change)) (cancel func()) { return e.buf.Subscribe(fn) }

// Register returns register r's contents.
func (e *Editor) Register(r rune) (Register, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.regs[r]
	return v, ok
}

func (e *Editor) SetRegister(r rune, v Register) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.regs[r] = v
}

// Echo shows a message, as vim's :echo and error messages do.
func (e *Editor) Echo(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.msgs = append(e.msgs, msg)
}

// Messages returns every message so far, oldest first.
func (e *Editor) Messages() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.msgs)
}
