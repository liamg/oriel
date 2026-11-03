package text

import (
	"bytes"
	"testing"
)

// checkTree checks the rope's invariants: leaves at one depth, sizes in bounds, summaries exact,
// author runs covering each leaf, and every leaf starting and ending between runes.
func checkTree(t testing.TB, s Snapshot) {
	t.Helper()
	all := s.Bytes()
	depth := -1
	var visit func(n *node, off, d int, root bool)
	visit = func(n *node, off, d int, root bool) {
		if n.height == 0 {
			if depth < 0 {
				depth = d
			} else if d != depth {
				t.Fatalf("leaves at depths %d and %d", depth, d)
			}
			if len(n.text) > maxLeaf+3 || (len(n.text) == 0 && !root) {
				t.Fatalf("leaf of %d bytes", len(n.text))
			}
			if got := measure(n.text); got != n.sum {
				t.Fatalf("leaf summary %+v, want %+v", n.sum, got)
			}
			total := 0
			for i, r := range n.runs {
				if r.n <= 0 || (i > 0 && n.runs[i-1].a == r.a) {
					t.Fatalf("leaf runs %v", n.runs)
				}
				total += r.n
			}
			if total != len(n.text) {
				t.Fatalf("runs cover %d of %d bytes", total, len(n.text))
			}
			if !bytes.Equal(all[off:off+len(n.text)], n.text) {
				t.Fatalf("leaf at %d does not match the text", off)
			}
			if !modelBoundary(all, off) || !modelBoundary(all, off+len(n.text)) {
				t.Fatalf("leaf %d:%d splits a rune in %q", off, off+len(n.text), all)
			}
			return
		}
		if len(n.kids) == 0 || len(n.kids) > maxKids || (root && len(n.kids) < 2) {
			t.Fatalf("inner node with %d kids", len(n.kids))
		}
		var sum summary
		for _, k := range n.kids {
			if k.height != n.height-1 {
				t.Fatalf("kid height %d under %d", k.height, n.height)
			}
			visit(k, off+sum.bytes, d+1, false)
			sum = sum.add(k.sum)
		}
		if sum != n.sum {
			t.Fatalf("inner summary %+v, want %+v", n.sum, sum)
		}
	}
	visit(s.r(), 0, 0, true)
}

func TestLargeTextBuildsABalancedTree(t *testing.T) {
	text := bytes.Repeat([]byte("héllo wörld 𝄞\n"), 100_000)
	s := New(text, Author{}).Snapshot()
	checkTree(t, s)
	if s.r().height > 4 {
		t.Fatalf("height %d for %d bytes", s.r().height, len(text))
	}
	if s.Lines() != 100_001 || s.LineStart(50_000) != 50_000*len("héllo wörld 𝄞\n") {
		t.Fatalf("Lines, LineStart = %d, %d", s.Lines(), s.LineStart(50_000))
	}
}
