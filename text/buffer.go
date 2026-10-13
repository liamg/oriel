package text

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	ErrInvalidEdit   = errors.New("text: invalid edit")
	ErrNothingToUndo = errors.New("text: nothing to undo")
	ErrConflict      = errors.New("text: later edits overlap the change")
)

// Replace swaps the text in Range for Text.
type Replace struct {
	Range Range
	Text  []byte
}

func (r Replace) String() string {
	return fmt.Sprintf("%d:%d→%q", r.Range.Start, r.Range.End, r.Text)
}

// Edit is one change to a buffer. Its ranges are offsets into the text before the edit, in order and not
// overlapping, and each starts and ends between runes. All of them make one undo step: that is multi-cursor.
type Edit struct {
	Ranges []Replace
	Author Author
	Group  UndoGroup // nonzero: coalesce with the previous edit in the same group, as typing does
}

// UndoGroup ties consecutive edits into one undo step. Get one from Buffer.NewGroup.
type UndoGroup uint64

// Cause is what made a change.
type Cause int

const (
	CauseEdit Cause = iota
	CauseUndo
	CauseRedo
	CauseUndoAuthor
)

// Change is what a buffer reports after it changes.
type Change struct {
	Before, After Snapshot
	// Replaces turn Before into After when applied in order, each in the text the one before it left.
	// They can be wider than the edit asked for: an edit that joins pieces of a UTF-8 sequence into one
	// rune is widened to the whole rune.
	Replaces []Replace
	Author   Author // the edit's author; for an undo or redo, the author of the step it moved over
	Cause    Cause
}

// AnchorID names a position that follows the text through every edit, whoever makes it.
type AnchorID uint64

type anchor struct {
	off  int
	bias Bias
}

// Buffer is the text of record for one file. It is safe for concurrent use.
type Buffer struct {
	mu      sync.Mutex
	snap    Snapshot
	anchors map[AnchorID]anchor
	nextID  AnchorID
	subs    []subscriber
	nextSub int
	group   UndoGroup
	undo    undoTree
}

type subscriber struct {
	id int
	fn func(Change)
}

// New returns a buffer holding content, written by a.
func New(content []byte, a Author) *Buffer {
	root := replaceRoot(emptyLeaf, 0, 0, newPiece(slices.Clone(content), a))
	b := &Buffer{snap: Snapshot{root: root}, anchors: map[AnchorID]anchor{}}
	b.undo.init()
	return b
}

func (b *Buffer) Snapshot() Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snap
}

// NewGroup returns an undo group no edit has used yet.
func (b *Buffer) NewGroup() UndoGroup {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.group++
	return b.group
}

// Apply makes an edit and records it for undo.
func (b *Buffer) Apply(e Edit) (Change, error) {
	b.mu.Lock()
	before := b.snap
	if err := checkEdit(before, e); err != nil {
		b.mu.Unlock()
		return Change{}, err
	}
	ops := make([]op, 0, len(e.Ranges))
	for _, r := range slices.Backward(e.Ranges) {
		ops = append(ops, b.replace(r.Range.Start, r.Range.End, newPiece(slices.Clone(r.Text), e.Author)))
	}
	b.undo.record(ops, e.Author, e.Group, nil)
	return b.commit(before, ops, e.Author, CauseEdit), nil
}

func checkEdit(s Snapshot, e Edit) error {
	prev := 0
	for _, r := range e.Ranges {
		switch {
		case r.Range.Start < prev || r.Range.Start > r.Range.End || r.Range.End > s.Len():
			return fmt.Errorf("%w: range %d:%d is out of order or out of bounds", ErrInvalidEdit, r.Range.Start, r.Range.End)
		case !s.Boundary(r.Range.Start) || !s.Boundary(r.Range.End):
			return fmt.Errorf("%w: range %d:%d splits a rune", ErrInvalidEdit, r.Range.Start, r.Range.End)
		}
		prev = r.Range.End
	}
	return nil
}

// op replaces del at at with ins: one replace as applied, kept for undo.
type op struct {
	at       int
	del, ins piece
}

func (o op) inverse() op { return op{at: o.at, del: o.ins, ins: o.del} }

// replace swaps [from, to) for p in the current snapshot, widened so that no UTF-8 sequence straddles
// either end of p, and moves the anchors. Callers hold mu.
func (b *Buffer) replace(from, to int, p piece) op {
	s := b.snap
	a, z := from, to
	left := s.Slice(Range{max(0, from-3), from})
	right := s.Slice(Range{to, min(s.Len(), to+3)})
	head := slices.Concat(left, p.text[:min(3, len(p.text))], right)
	if back, _ := straddle(head[:min(len(head), len(left)+3)], len(left)); back > 0 {
		a = from - back
	}
	tail := slices.Concat(left, p.text)
	tail = tail[max(0, len(tail)-3):]
	if _, fwd := straddle(slices.Concat(tail, right), len(tail)); fwd > 0 {
		z = to + fwd
	}
	ins := cat(s.piece(Range{a, from}), p, s.piece(Range{to, z}))
	o := op{at: a, del: s.piece(Range{a, z}), ins: ins}
	b.snap = Snapshot{root: replaceRoot(s.r(), a, z, ins), version: s.version}
	for id, an := range b.anchors {
		an.off = mapOffset(an.off, an.bias, a, z, len(ins.text))
		b.anchors[id] = an
	}
	return o
}

// mapOffset moves an offset through a replace of [from, to) by n bytes. An offset at either end of the
// replaced text keeps its side; one inside it, or at a pure insertion, goes to the side its bias says.
func mapOffset(off int, bias Bias, from, to, n int) int {
	switch {
	case off < from:
		return off
	case off > to:
		return off + n - (to - from)
	case from == to:
		if bias == Left {
			return from
		}
		return from + n
	case off == from:
		return from
	case off == to:
		return from + n
	case bias == Left:
		return from
	default:
		return from + n
	}
}

func (b *Buffer) play(ops []op) []op {
	out := make([]op, 0, len(ops))
	for _, o := range ops {
		out = append(out, b.replace(o.at, o.at+len(o.del.text), o.ins))
	}
	return out
}

// commit bumps the version, unlocks and tells the subscribers.
func (b *Buffer) commit(before Snapshot, ops []op, a Author, cause Cause) Change {
	b.snap.version = before.version + 1
	ch := Change{Before: before, After: b.snap, Author: a, Cause: cause, Replaces: make([]Replace, len(ops))}
	for i, o := range ops {
		ch.Replaces[i] = Replace{Range: Range{o.at, o.at + len(o.del.text)}, Text: o.ins.text}
	}
	subs := slices.Clone(b.subs)
	b.mu.Unlock()
	for _, s := range subs {
		s.fn(ch)
	}
	return ch
}

// Subscribe calls fn after every change, on the goroutine that made it. Changes made concurrently can
// arrive out of order; Change.After.Version orders them.
func (b *Buffer) Subscribe(fn func(Change)) (cancel func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextSub++
	id := b.nextSub
	b.subs = append(b.subs, subscriber{id, fn})
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.subs = slices.DeleteFunc(b.subs, func(s subscriber) bool { return s.id == id })
	}
}

// Anchor starts tracking off, which is moved back to the nearest rune boundary if it isn't on one.
func (b *Buffer) Anchor(off int, bias Bias) AnchorID {
	b.mu.Lock()
	defer b.mu.Unlock()
	off = min(max(off, 0), b.snap.Len())
	for !b.snap.Boundary(off) {
		off--
	}
	b.nextID++
	b.anchors[b.nextID] = anchor{off, bias}
	return b.nextID
}

// Resolve returns an anchor's offset in the current snapshot.
func (b *Buffer) Resolve(id AnchorID) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	an, ok := b.anchors[id]
	return an.off, ok
}

// Release stops tracking an anchor.
func (b *Buffer) Release(id AnchorID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.anchors, id)
}
