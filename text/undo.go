package text

import (
	"fmt"
	"slices"
	"time"
)

// step is one node of the undo tree: the ops that took its parent's text to its own.
type step struct {
	seq     int
	parent  *step
	kids    []*step
	redo    *step // the child Redo goes to: the one last undone from, else the newest
	author  Author
	group   UndoGroup
	ops     []op
	reverts *step // set on a step made by UndoAuthor
	time    time.Time
}

type undoTree struct {
	root, head *step
	steps      []*step // by seq
}

func (u *undoTree) init() {
	u.root = &step{}
	u.head = u.root
	u.steps = []*step{u.root}
}

func (u *undoTree) record(ops []op, a Author, g UndoGroup, reverts *step) {
	if h := u.head; g != 0 && h != u.root && h.group == g && h.author == a && h.reverts == nil && len(h.kids) == 0 {
		h.ops = append(h.ops, ops...)
		return
	}
	s := &step{seq: len(u.steps), parent: u.head, author: a, group: g, ops: ops, reverts: reverts, time: time.Now()}
	u.head.kids = append(u.head.kids, s)
	u.head.redo = s
	u.head = s
	u.steps = append(u.steps, s)
}

func inverse(ops []op) []op {
	out := make([]op, len(ops))
	for i, o := range slices.Backward(ops) {
		out[len(ops)-1-i] = o.inverse()
	}
	return out
}

// Undo moves to the parent of the current undo step.
func (b *Buffer) Undo() (Change, bool) {
	b.mu.Lock()
	h := b.undo.head
	if h == b.undo.root {
		b.mu.Unlock()
		return Change{}, false
	}
	before := b.snap
	ops := b.play(inverse(h.ops))
	h.parent.redo = h
	b.undo.head = h.parent
	return b.commit(before, ops, h.author, CauseUndo), true
}

// Redo moves to the child of the current undo step that was last undone from.
func (b *Buffer) Redo() (Change, bool) {
	b.mu.Lock()
	n := b.undo.head.redo
	if n == nil {
		b.mu.Unlock()
		return Change{}, false
	}
	before := b.snap
	ops := b.play(n.ops)
	b.undo.head = n
	return b.commit(before, ops, n.author, CauseRedo), true
}

// UndoSeq is the number of the current undo step; 0 is the text as loaded.
func (b *Buffer) UndoSeq() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.undo.head.seq
}

// UndoParent returns the step that step seq was made on.
func (b *Buffer) UndoParent(seq int) (int, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if seq <= 0 || seq >= len(b.undo.steps) {
		return 0, false
	}
	return b.undo.steps[seq].parent.seq, true
}

// UndoTo moves through the tree to step seq, on whichever branch it is.
func (b *Buffer) UndoTo(seq int) (Change, error) {
	b.mu.Lock()
	if seq < 0 || seq >= len(b.undo.steps) {
		b.mu.Unlock()
		return Change{}, fmt.Errorf("%w: no undo step %d", ErrNothingToUndo, seq)
	}
	target := b.undo.steps[seq]
	onPath := map[*step]bool{}
	for s := target; s != nil; s = s.parent {
		onPath[s] = true
	}
	before := b.snap
	var ops []op
	for !onPath[b.undo.head] {
		h := b.undo.head
		ops = append(ops, b.play(inverse(h.ops))...)
		h.parent.redo = h
		b.undo.head = h.parent
	}
	var down []*step
	for s := target; s != b.undo.head; s = s.parent {
		down = append(down, s)
	}
	for _, s := range slices.Backward(down) {
		s.parent.redo = s
		ops = append(ops, b.play(s.ops)...)
	}
	b.undo.head = target
	return b.commit(before, ops, target.author, CauseUndo), nil
}

// UndoAuthor reverts the latest step by author a that is still in the text, keeping every later edit,
// and records the revert as a new step by by. It fails with ErrConflict when a later edit overlaps it.
func (b *Buffer) UndoAuthor(a, by Author) (Change, error) {
	b.mu.Lock()
	var later []*step
	reverted := map[*step]bool{}
	var target *step
	for s := b.undo.head; s != b.undo.root; s = s.parent {
		if s.reverts != nil {
			reverted[s.reverts] = true
		} else if s.author == a && !reverted[s] {
			target = s
			break
		}
		later = append(later, s)
	}
	if target == nil {
		b.mu.Unlock()
		return Change{}, fmt.Errorf("%w by %s", ErrNothingToUndo, a)
	}
	var after []op
	for _, s := range slices.Backward(later) {
		after = append(after, s.ops...)
	}
	var revert []op
	for _, o := range inverse(target.ops) {
		for i, l := range after {
			var ok bool
			if o, after[i], ok = transform(o, l); !ok {
				b.mu.Unlock()
				return Change{}, fmt.Errorf("%w: undoing %s's step %d", ErrConflict, a, target.seq)
			}
		}
		revert = append(revert, o)
	}
	before := b.snap
	ops := b.play(revert)
	b.undo.record(ops, by, 0, target)
	return b.commit(before, ops, by, CauseUndoAuthor), nil
}

// transform takes two ops made on the same text and returns each as it applies after the other.
// It fails when they overlap; ops that only touch keep their order, and x goes first at a tie.
func transform(x, y op) (op, op, bool) {
	xEnd, yEnd := x.at+len(x.del.text), y.at+len(y.del.text)
	switch {
	case xEnd <= y.at:
		y.at += len(x.ins.text) - len(x.del.text)
		return x, y, true
	case yEnd <= x.at:
		x.at += len(y.ins.text) - len(y.del.text)
		return x, y, true
	}
	return x, y, false
}
