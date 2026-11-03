// Package edit holds the editing primitives every input engine drives: cursors, edits that go through
// undo and authorship, registers, marks and messages. Engines never touch text.Buffer directly.
package edit

import (
	"slices"
	"strings"
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

// Cursor is a cursor's offset in the buffer and, when it's selecting, where the selection started: the text
// between Anchor and Off is selected.
type Cursor struct {
	Off       int
	Anchor    int
	Selecting bool
}

// Selection is the selected range, in order, and whether there is one.
func (c Cursor) Selection() (text.Range, bool) {
	if !c.Selecting || c.Anchor == c.Off {
		return text.Range{}, false
	}
	return text.Range{Start: min(c.Anchor, c.Off), End: max(c.Anchor, c.Off)}, true
}

// Editor is one window's view of a buffer: its cursors, plus the registers and notices it shares
// with whatever else edits through it.
type Editor struct {
	buf    *text.Buffer
	author text.Author
	shared *shared

	mu      sync.Mutex
	cursors []Cursor
}

// shared is what every editor opened from the same one shares: registers, the clipboard and notices.
type shared struct {
	mu      sync.Mutex
	regs    map[rune]Register
	notices []Notice
	clip    Clipboard
	notify  func(Notice)
}

// Clipboard is the system clipboard, as the IDE provides it. Primary is X11's primary selection, which
// platforms without one treat as the clipboard.
type Clipboard interface {
	Read(primary bool) (string, bool)
	Write(primary bool, text string)
}

// New returns an editor over buf whose edits are by author.
func New(buf *text.Buffer, author text.Author) *Editor {
	return &Editor{buf: buf, author: author, cursors: []Cursor{{}}, shared: &shared{regs: map[rune]Register{}}}
}

// Open returns an editor over buf, another buffer or the same one in another window, that shares this
// editor's registers and notices.
func (e *Editor) Open(buf *text.Buffer) *Editor {
	return &Editor{buf: buf, author: e.author, cursors: []Cursor{{}}, shared: e.shared}
}

// Buffer is the buffer the editor edits.
func (e *Editor) Buffer() *text.Buffer { return e.buf }

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

// SetClipboard makes "+ and "* the system clipboard (and the primary selection); nil makes them ordinary registers.
func (e *Editor) SetClipboard(c Clipboard) {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	e.shared.clip = c
}

// HasClipboard reports whether "+ and "* are the system's.
func (e *Editor) HasClipboard() bool {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	return e.shared.clip != nil
}

// Register returns register r's contents. With a clipboard, "+ and "* read it: text that's still what was last
// written keeps the kind it was written with, and other text is linewise when it ends in a newline.
func (e *Editor) Register(r rune) (Register, bool) {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	if c := e.shared.clip; c != nil && (r == '+' || r == '*') {
		text, ok := c.Read(r == '*')
		if !ok {
			return Register{}, false
		}
		if last, ok := e.shared.regs[r]; ok && last.Text == text {
			return last, true
		}
		if strings.HasSuffix(text, "\n") {
			return Register{Text: text, Kind: Linewise}, true
		}
		return Register{Text: text}, true
	}
	v, ok := e.shared.regs[r]
	return v, ok
}

func (e *Editor) SetRegister(r rune, v Register) {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	e.shared.regs[r] = v
	if c := e.shared.clip; c != nil && (r == '+' || r == '*') {
		c.Write(r == '*', v.Text)
	}
}

// Notify gives the IDE a notice to show.
func (e *Editor) Notify(n Notice) {
	e.shared.mu.Lock()
	e.shared.notices = append(e.shared.notices, n)
	fn := e.shared.notify
	e.shared.mu.Unlock()
	if fn != nil {
		fn(n)
	}
}

// OnNotice calls fn with every notice from now on, on the goroutine that gave it.
func (e *Editor) OnNotice(fn func(Notice)) {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	e.shared.notify = fn
}

// Notices returns every notice so far, oldest first.
func (e *Editor) Notices() []Notice {
	e.shared.mu.Lock()
	defer e.shared.mu.Unlock()
	return slices.Clone(e.shared.notices)
}
