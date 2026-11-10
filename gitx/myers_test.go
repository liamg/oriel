package gitx

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"testing"
)

// fullTrace is myers as it was first written, keeping all of v at every step: the reference the windowed trace
// must agree with.
func fullTrace(a, b []string) []byte {
	n, m := len(a), len(b)
	most := n + m
	if most == 0 {
		return nil
	}
	off := most
	v := make([]int, 2*most+2)
	var trace [][]int
	for d := 0; d <= most; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || k != d && v[off+k-1] < v[off+k+1] {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				ds := make([]diagonals, len(trace))
				for i, t := range trace {
					ds[i] = diagonals{v: t}
				}
				return backtrack(ds, d, n, m, off)
			}
		}
	}
	return nil
}

// The windowed trace gives the same edit script as keeping all of v, for any pair of texts.
func TestMyersMatchesFullTrace(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	words := []string{"a", "b", "c", "d", "e"}
	text := func() []string {
		out := make([]string, r.IntN(40))
		for i := range out {
			out[i] = words[r.IntN(len(words))]
		}
		return out
	}
	for i := range 5000 {
		a, b := text(), text()
		if got, want := myers(a, b), fullTrace(a, b); !bytes.Equal(got, want) {
			t.Fatalf("case %d, %q → %q: %q, want %q", i, a, b, got, want)
		}
	}
}

// myersLong is the long inputs Myers must diff in bounded memory: a new file, and a rewrite.
func myersLong() []struct {
	name string
	a, b []string
} {
	lines := func(n int, f string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf(f, i)
		}
		return out
	}
	return []struct {
		name string
		a, b []string
	}{
		{"a new file", nil, lines(20000, "new %d")},
		{"a rewrite", lines(20000, "old %d"), lines(20000, "new %d")},
	}
}

// Long inputs with nothing in common diff to every line out and every line in (TestMyersMemoryBudget holds what
// that allocates).
func TestMyersLong(t *testing.T) {
	for _, tt := range myersLong() {
		if ops := myers(tt.a, tt.b); len(ops) != len(tt.a)+len(tt.b) {
			t.Errorf("%s: %d ops for %d lines out and %d in", tt.name, len(ops), len(tt.a), len(tt.b))
		}
	}
}
