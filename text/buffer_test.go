package text

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

var (
	you   = Author{Kind: User}
	agent = Author{Kind: Agent, ID: "s1"}
)

func ins(at int, s string) Replace       { return Replace{Range: Range{at, at}, Text: []byte(s)} }
func del(from, to int) Replace           { return Replace{Range: Range{from, to}} }
func rep(from, to int, s string) Replace { return Replace{Range: Range{from, to}, Text: []byte(s)} }

func apply(t *testing.T, b *Buffer, a Author, rs ...Replace) Change {
	t.Helper()
	ch, err := b.Apply(Edit{Ranges: rs, Author: a})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func wantText(t *testing.T, b *Buffer, want string) {
	t.Helper()
	if got := b.Snapshot().String(); got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestMultiRangeEditIsOneUndoStep(t *testing.T) {
	b := New([]byte("a\nb\nc"), Author{})
	apply(t, b, you, ins(0, "> "), ins(2, "> "), ins(4, "> "))
	wantText(t, b, "> a\n> b\n> c")
	b.Undo()
	wantText(t, b, "a\nb\nc")
	b.Redo()
	wantText(t, b, "> a\n> b\n> c")
}

func TestGroupCoalescesTyping(t *testing.T) {
	b := New(nil, Author{})
	g := b.NewGroup()
	for i, c := range "hello" {
		if _, err := b.Apply(Edit{Ranges: []Replace{ins(i, string(c))}, Author: you, Group: g}); err != nil {
			t.Fatal(err)
		}
	}
	apply(t, b, you, ins(5, "!"))
	b.Undo()
	wantText(t, b, "hello")
	b.Undo()
	wantText(t, b, "")
	if _, ok := b.Undo(); ok {
		t.Fatal("undo past the start")
	}
}

func TestUndoTreeKeepsBranches(t *testing.T) {
	b := New([]byte("x"), Author{})
	apply(t, b, you, ins(1, "1"))
	b.Undo()
	apply(t, b, you, ins(1, "2"))
	if got := b.UndoSeq(); got != 2 {
		t.Fatalf("seq = %d, want 2", got)
	}
	if p, ok := b.UndoParent(2); !ok || p != 0 {
		t.Fatalf("UndoParent(2) = %d, %v; want 0, the branch point", p, ok)
	}
	b.Undo()
	b.Redo()
	wantText(t, b, "x2") // redo follows the branch last undone from
	if _, err := b.UndoTo(1); err != nil {
		t.Fatal(err)
	}
	wantText(t, b, "x1")
	if _, err := b.UndoTo(0); err != nil {
		t.Fatal(err)
	}
	wantText(t, b, "x")
}

func TestAuthorship(t *testing.T) {
	b := New([]byte("func f() {}\n"), Author{})
	apply(t, b, agent, ins(10, "\n\treturn\n"))
	got := slices.Collect(b.Snapshot().Authors(Range{0, b.Snapshot().Len()}))
	want := []AuthorSpan{{Range{0, 10}, Author{}}, {Range{10, 19}, agent}, {Range{19, 21}, Author{}}}
	if !slices.Equal(got, want) {
		t.Fatalf("authors = %v, want %v", got, want)
	}
	b.Undo()
	b.Redo()
	if got2 := slices.Collect(b.Snapshot().Authors(Range{0, b.Snapshot().Len()})); !slices.Equal(got2, want) {
		t.Fatalf("authors after undo, redo = %v, want %v", got2, want)
	}
}

func TestUndoAuthorRevertsOnlyThatAuthor(t *testing.T) {
	b := New([]byte("one\ntwo\nthree\n"), Author{})
	apply(t, b, agent, rep(4, 7, "TWO"))
	apply(t, b, you, ins(0, "// "), ins(14, "end\n"))
	ch, err := b.UndoAuthor(agent, you)
	if err != nil {
		t.Fatal(err)
	}
	wantText(t, b, "// one\ntwo\nthree\nend\n")
	if ch.Author != you || ch.Cause != CauseUndoAuthor {
		t.Fatalf("change = %+v", ch)
	}
	if _, err := b.UndoAuthor(agent, you); !errors.Is(err, ErrNothingToUndo) {
		t.Fatalf("second UndoAuthor = %v, want ErrNothingToUndo", err)
	}
	b.Undo() // the revert is an ordinary step
	wantText(t, b, "// one\nTWO\nthree\nend\n")
}

func TestUndoAuthorConflict(t *testing.T) {
	b := New([]byte("one two"), Author{})
	apply(t, b, agent, rep(4, 7, "TWO"))
	apply(t, b, you, rep(5, 6, "w"))
	if _, err := b.UndoAuthor(agent, you); !errors.Is(err, ErrConflict) {
		t.Fatalf("UndoAuthor over an overlapping later edit = %v, want ErrConflict", err)
	}
	wantText(t, b, "one TwO")
}

func TestUndoAuthorTouchingEditsDoNotConflict(t *testing.T) {
	b := New([]byte("ab"), Author{})
	apply(t, b, agent, ins(1, "XY"))
	apply(t, b, you, ins(3, "!"))
	if _, err := b.UndoAuthor(agent, you); err != nil {
		t.Fatal(err)
	}
	wantText(t, b, "a!b")
}

func TestAnchorBias(t *testing.T) {
	b := New([]byte("abcdef"), Author{})
	l, r := b.Anchor(2, Left), b.Anchor(2, Right)
	in := b.Anchor(4, Right)
	apply(t, b, you, ins(2, "XX"))
	resolve := func(id AnchorID) int {
		off, ok := b.Resolve(id)
		if !ok {
			t.Fatalf("anchor %d gone", id)
		}
		return off
	}
	if resolve(l) != 2 || resolve(r) != 4 || resolve(in) != 6 {
		t.Fatalf("after insert: %d %d %d, want 2 4 6", resolve(l), resolve(r), resolve(in))
	}
	apply(t, b, you, del(3, 7)) // "abXXcdef" → "abXf": the anchor at 6 was inside
	if resolve(in) != 3 {
		t.Fatalf("anchor inside a deletion = %d, want 3", resolve(in))
	}
}

func TestSubscribe(t *testing.T) {
	b := New([]byte("a"), Author{})
	var got []Change
	cancel := b.Subscribe(func(c Change) { got = append(got, c) })
	apply(t, b, agent, ins(1, "b"))
	b.Undo()
	cancel()
	apply(t, b, you, ins(0, "c"))
	if len(got) != 2 || got[0].Cause != CauseEdit || got[0].Author != agent || got[1].Cause != CauseUndo {
		t.Fatalf("changes = %+v", got)
	}
}

func TestSnapshotsAreSafeAcrossGoroutines(t *testing.T) {
	b := New([]byte("start\n"), Author{})
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 200 {
				s := b.Snapshot()
				_ = s.Line(s.Lines() - 1)
				if w == 0 {
					b.Apply(Edit{Ranges: []Replace{ins(s.Len(), "x")}, Author: you}) // may be stale after an undo: errors are fine

				}
				if w == 1 && i%10 == 0 {
					b.Undo()
				}
			}
		})
	}
	wg.Wait()
}
