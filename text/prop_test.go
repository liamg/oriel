package text

import (
	"bytes"
	"github.com/liamg/oriel/internal/testhook"
	"math/rand/v2"
	"slices"
	"testing"
	"unicode/utf8"
)

// source supplies the choices a run makes: random in the property tests, the fuzzer's bytes in FuzzBuffer.
type source interface {
	intn(n int) int
	done() bool
}

type randSource struct{ r *rand.Rand }

func (s randSource) intn(n int) int { return s.r.IntN(n) }
func (s randSource) done() bool     { return false }

type byteSource struct {
	b []byte
	i int
}

func (s *byteSource) intn(n int) int {
	if s.i >= len(s.b) {
		return 0
	}
	v := int(s.b[s.i])
	s.i++
	if n > 256 && s.i < len(s.b) {
		v = v<<8 | int(s.b[s.i])
		s.i++
	}
	return v % n
}
func (s *byteSource) done() bool { return s.i >= len(s.b) }

// alphabet is weighted towards what breaks ropes: newlines, multibyte runes, astral runes (two UTF-16 units),
// and lone pieces of UTF-8 sequences that can join up with their neighbours.
var alphabet = [][]byte{
	[]byte("a"), []byte("b"), []byte(" "), []byte("\n"), []byte("\n"), []byte("é"), []byte("€"), []byte("𝄞"),
	{0xe2}, {0x82}, {0xac}, {0xf0}, {0x9d}, {0x84}, {0x9e}, {0xc3}, {0xa9}, {0xff}, []byte("\r\n"), []byte("xyz"),
}

func genText(s source, max int) []byte {
	n := s.intn(max + 1)
	var b []byte
	for len(b) < n {
		b = append(b, alphabet[s.intn(len(alphabet))]...)
	}
	return b
}

var authors = []Author{{Kind: User}, {Kind: Agent, ID: "s1"}, {Kind: Agent, ID: "s2"}, {Kind: LSP}, {Kind: Format}}

// boundaryNear moves off back to the nearest offset that is between runes.
func boundaryNear(b []byte, off int) int {
	for !modelBoundary(b, off) {
		off--
	}
	return off
}

type harness struct {
	t      testing.TB
	s      source
	m      *model
	b      *Buffer
	old    []oldSnap
	groups []UndoGroup
}

type oldSnap struct {
	snap Snapshot
	text []byte
}

func newHarness(t testing.TB, s source, maxText int) *harness {
	content := genText(s, maxText)
	h := &harness{t: t, s: s, m: newModel(content, Author{}), b: New(content, Author{})}
	h.check("new")
	return h
}

func (h *harness) step() {
	t, s, m, b := h.t, h.s, h.m, h.b
	t.Helper()
	switch op := s.intn(10); op {
	case 0, 1, 2, 3:
		e := Edit{Author: authors[s.intn(len(authors))]}
		if s.intn(3) == 0 {
			if len(h.groups) == 0 || s.intn(4) == 0 {
				h.groups = append(h.groups, b.NewGroup())
			}
			e.Group = h.groups[len(h.groups)-1]
		}
		var offs []int
		for range 1 + s.intn(3) {
			offs = append(offs, boundaryNear(m.text, s.intn(len(m.text)+1)))
		}
		if len(offs)%2 == 1 {
			offs = append(offs, offs[len(offs)-1])
		}
		slices.Sort(offs)
		for i := 0; i < len(offs); i += 2 {
			e.Ranges = append(e.Ranges, Replace{Range: Range{offs[i], offs[i+1]}, Text: genText(s, 12)})
		}
		if s.intn(20) == 0 {
			e.Ranges[0].Text = genText(s, 3000)
		}
		ch, err := b.Apply(e)
		if err != nil {
			t.Fatalf("Apply(%v): %v", e, err)
		}
		before := slices.Clone(m.text)
		m.apply(e)
		if !bytes.Equal(ch.Before.Bytes(), before) {
			t.Fatalf("Change.Before is not the text before the edit")
		}
		h.checkChange(ch)
		h.check("apply")
	case 4:
		ch, ok := b.Undo()
		if mok := m.undo(); ok != mok {
			t.Fatalf("Undo() = %v, model %v", ok, mok)
		}
		if ok {
			h.checkChange(ch)
		}
		h.check("undo")
	case 5:
		ch, ok := b.Redo()
		if mok := m.redo(); ok != mok {
			t.Fatalf("Redo() = %v, model %v", ok, mok)
		}
		if ok {
			h.checkChange(ch)
		}
		h.check("redo")
	case 6:
		off := boundaryNear(m.text, s.intn(len(m.text)+1))
		bias := Bias(s.intn(2))
		id := b.Anchor(off, bias)
		if _, dup := m.anchors[id]; dup {
			t.Fatalf("Anchor returned a live id %d again", id)
		}
		m.anchors[id] = modelAnchor{off, bias}
	case 7:
		if len(m.anchors) > 0 {
			ids := slices.Sorted(mapKeys(m.anchors))
			id := ids[s.intn(len(ids))]
			b.Release(id)
			delete(m.anchors, id)
			if _, ok := b.Resolve(id); ok {
				t.Fatalf("Resolve(%d) after Release = ok", id)
			}
		}
	case 8:
		h.old = append(h.old, oldSnap{b.Snapshot(), slices.Clone(m.text)})
		if len(h.old) > 8 {
			h.old = h.old[1:]
		}
	case 9:
		h.queries()
	}
}

func mapKeys[K comparable, V any](m map[K]V) func(func(K) bool) {
	return func(yield func(K) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// checkChange replays the change's replaces on its Before text and expects its After text.
func (h *harness) checkChange(ch Change) {
	h.t.Helper()
	got := ch.Before.Bytes()
	for _, r := range ch.Replaces {
		got = slices.Concat(got[:r.Range.Start], r.Text, got[r.Range.End:])
	}
	if !bytes.Equal(got, ch.After.Bytes()) {
		h.t.Fatalf("replaying Change.Replaces on Before gives %q, After is %q", got, ch.After.Bytes())
	}
	if ch.After.Version() <= ch.Before.Version() {
		h.t.Fatalf("version did not advance: %d → %d", ch.Before.Version(), ch.After.Version())
	}
}

func (h *harness) check(what string) {
	t, m := h.t, h.m
	t.Helper()
	snap := h.b.Snapshot()
	if got := snap.Bytes(); !bytes.Equal(got, m.text) {
		t.Fatalf("after %s: text %q, want %q", what, got, m.text)
	}
	if snap.Len() != len(m.text) || snap.Lines() != m.lines() {
		t.Fatalf("after %s: Len, Lines = %d, %d; want %d, %d", what, snap.Len(), snap.Lines(), len(m.text), m.lines())
	}
	if got, want := slices.Collect(snap.Authors(Range{0, len(m.text)})), m.authorRuns(0, len(m.text)); !slices.Equal(got, want) {
		t.Fatalf("after %s: authors %v, want %v", what, got, want)
	}
	for id, an := range m.anchors {
		if got, ok := h.b.Resolve(id); !ok || got != an.off {
			t.Fatalf("after %s: Resolve(%d) = %d, %v; want %d", what, id, got, ok, an.off)
		}
	}
	checkTree(t, snap)
	for _, o := range h.old {
		if !bytes.Equal(o.snap.Bytes(), o.text) {
			t.Fatalf("an old snapshot changed: %q, want %q", o.snap.Bytes(), o.text)
		}
	}
}

func (h *harness) queries() {
	t, s, m := h.t, h.s, h.m
	t.Helper()
	snap := h.b.Snapshot()
	for range 4 {
		n := s.intn(m.lines())
		if got, want := snap.Line(n), m.line(n); !bytes.Equal(got, want) {
			t.Fatalf("Line(%d) = %q, want %q", n, got, want)
		}
		if got, want := snap.LineStart(n), m.lineStart(n); got != want {
			t.Fatalf("LineStart(%d) = %d, want %d", n, got, want)
		}

		off := boundaryNear(m.text, s.intn(len(m.text)+1))
		p := snap.PosAt(off)
		if want := m.posAt(off); p != want {
			t.Fatalf("PosAt(%d) = %v, want %v", off, p, want)
		}
		if got := snap.Offset(p); got != off {
			t.Fatalf("Offset(%v) = %d, want %d", p, got, off)
		}
		u := snap.UTF16(p)
		if want := utf16Len(m.line(p.Line)[:p.Col]); u != want {
			t.Fatalf("UTF16(%v) = %d, want %d", p, u, want)
		}
		if got := snap.PosFromUTF16(p.Line, u); got != p {
			t.Fatalf("PosFromUTF16(%d, %d) = %v, want %v", p.Line, u, got, p)
		}
		if got, want := snap.Boundary(off), true; got != want {
			t.Fatalf("Boundary(%d) = false at a rune boundary", off)
		}
		if raw := s.intn(len(m.text) + 1); snap.Boundary(raw) != modelBoundary(m.text, raw) {
			t.Fatalf("Boundary(%d) = %v, want %v", raw, snap.Boundary(raw), modelBoundary(m.text, raw))
		}

		from, to := off, boundaryNear(m.text, s.intn(len(m.text)+1))
		if from > to {
			from, to = to, from
		}
		if got := snap.Slice(Range{from, to}); !bytes.Equal(got, m.text[from:to]) {
			t.Fatalf("Slice(%d, %d) = %q, want %q", from, to, got, m.text[from:to])
		}
		if got, want := slices.Collect(snap.Authors(Range{from, to})), m.authorRuns(from, to); !slices.Equal(got, want) {
			t.Fatalf("Authors(%d, %d) = %v, want %v", from, to, got, want)
		}

		offs, rs := m.runes()
		i, _ := slices.BinarySearch(offs, off)
		var gotOffs []int
		var gotRunes []rune
		for o, r := range snap.Runes(off, Forward) {
			gotOffs, gotRunes = append(gotOffs, o), append(gotRunes, r)
			if len(gotOffs) == 20 {
				break
			}
		}
		end := min(i+20, len(offs))
		if !slices.Equal(gotOffs, offs[i:end]) || !slices.Equal(gotRunes, rs[i:end]) {
			t.Fatalf("Runes(%d, Forward) = %v %q, want %v %q", off, gotOffs, gotRunes, offs[i:end], rs[i:end])
		}
		gotOffs, gotRunes = nil, nil
		for o, r := range snap.Runes(off, Backward) {
			gotOffs, gotRunes = append(gotOffs, o), append(gotRunes, r)
			if len(gotOffs) == 20 {
				break
			}
		}
		wantOffs, wantRunes := slices.Clone(offs[max(0, i-20):i]), slices.Clone(rs[max(0, i-20):i])
		slices.Reverse(wantOffs)
		slices.Reverse(wantRunes)
		if !slices.Equal(gotOffs, wantOffs) || !slices.Equal(gotRunes, wantRunes) {
			t.Fatalf("Runes(%d, Backward) = %v %q, want %v %q", off, gotOffs, gotRunes, wantOffs, wantRunes)
		}
	}
}

// withLimits shrinks the rope's node sizes so that small texts build deep trees.
func withLimits(t testing.TB, leaf, minLeafSize, kids int) {
	testhook.Set(t, &maxLeaf, leaf)
	testhook.Set(t, &minLeaf, minLeafSize)
	testhook.Set(t, &maxKids, kids)
}

func TestBufferMatchesModel(t *testing.T) {
	for _, lim := range []struct {
		name            string
		leaf, min, kids int
	}{
		{"default", maxLeaf, minLeaf, maxKids},
		{"tiny", 8, 2, 3},
		{"small", 32, 8, 4},
	} {
		t.Run(lim.name, func(t *testing.T) {
			withLimits(t, lim.leaf, lim.min, lim.kids)
			for seed := range uint64(150) {
				h := newHarness(t, randSource{rand.New(rand.NewPCG(seed, 7))}, 400)
				for range 300 {
					h.step()
				}
			}
		})
	}
}

func FuzzBuffer(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{3, 0xe2, 0, 5, 9, 4, 4, 5})
	f.Add(bytes.Repeat([]byte{1, 2, 3, 7, 6, 4, 5, 9}, 40))
	f.Fuzz(func(t *testing.T, data []byte) {
		withLimits(t, 8, 2, 3)
		s := &byteSource{b: data}
		h := newHarness(t, s, 200)
		for !s.done() {
			h.step()
		}
		h.queries()
	})
}

func TestUTF8NeverSplitAcrossLeaves(t *testing.T) {
	withLimits(t, 4, 1, 3)
	// "€" is e2 82 ac. Deleting the x between its pieces joins them into one rune.
	b := New([]byte("ab\xe2x\x82\xacyz"), Author{})
	ch, err := b.Apply(Edit{Ranges: []Replace{{Range: Range{3, 4}}}, Author: Author{Kind: User}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ch.After.String(); got != "ab€yz" {
		t.Fatalf("text = %q", got)
	}
	if want := []Replace{{Range: Range{2, 6}, Text: []byte("€")}}; !slices.EqualFunc(ch.Replaces, want, replaceEqual) {
		t.Fatalf("Replaces = %v, want the edit widened to the whole rune: %v", ch.Replaces, want)
	}
	checkTree(t, ch.After)
	if got := ch.After.UTF16(Pos{0, 5}); got != 3 {
		t.Fatalf("UTF16 after the join = %d, want 3", got)
	}
}

func replaceEqual(a, b Replace) bool { return a.Range == b.Range && bytes.Equal(a.Text, b.Text) }

func TestApplyRejectsBadEdits(t *testing.T) {
	b := New([]byte("a€b"), Author{})
	for _, e := range []Edit{
		{Ranges: []Replace{{Range: Range{0, 9}}}},
		{Ranges: []Replace{{Range: Range{2, 1}}}},
		{Ranges: []Replace{{Range: Range{2, 2}}}},                       // inside €
		{Ranges: []Replace{{Range: Range{1, 3}}}},                       // ends inside €
		{Ranges: []Replace{{Range: Range{1, 4}}, {Range: Range{0, 1}}}}, // out of order
		{Ranges: []Replace{{Range: Range{0, 2}}, {Range: Range{1, 4}}}}, // overlapping
	} {
		if _, err := b.Apply(e); err == nil {
			t.Errorf("Apply(%v) = nil error", e)
		}
	}
	if b.Snapshot().String() != "a€b" || b.Snapshot().Version() != 0 {
		t.Fatalf("a rejected edit changed the buffer")
	}
}

func TestLinesAndUTF16(t *testing.T) {
	s := New([]byte("a𝄞b\n\nlast"), Author{}).Snapshot()
	if s.Lines() != 3 {
		t.Fatalf("Lines = %d", s.Lines())
	}
	if got := s.UTF16(Pos{0, 5}); got != 3 { // a, 𝄞 (two units), then before b
		t.Fatalf("UTF16 = %d", got)
	}
	if got := s.PosFromUTF16(0, 2); got != (Pos{0, 1}) { // inside the surrogate pair: back to its start
		t.Fatalf("PosFromUTF16 inside a pair = %v", got)
	}
	if got := s.PosFromUTF16(0, 99); got != (Pos{0, 6}) {
		t.Fatalf("PosFromUTF16 past the end = %v", got)
	}
	if !utf8.Valid(s.Line(0)) || string(s.Line(2)) != "last" || len(s.Line(1)) != 0 {
		t.Fatalf("Line()")
	}
}
