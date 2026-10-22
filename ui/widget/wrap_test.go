package widget

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
)

var wrapFont = typeset.FontSpec{Family: typeset.UI, Size: 12.5}

func bundled(t testing.TB) *typeset.Typesetter {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ts.Close)
	return ts
}

func TestWrapLinesBreaksAtSpacesAndInsideLongWords(t *testing.T) {
	sh := headless.Shaper{} // 0.6em per rune: 10px at size 10/0.6
	f := typeset.FontSpec{Size: 10 / 0.6}
	got := WrapLines(sh, "aa bb  cc\n\ndddddddddd e", 50, f)
	want := []string{"aa bb", "cc", "", "ddddd", "ddddd", "e"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestWrapLinesFitsWidth(t *testing.T) {
	ts := bundled(t)
	s := "The quick brown fox jumps over the lazy dog. " + strings.Repeat("Wi", 300) + " and then some ordinary words afterwards, ffi fl."
	for _, w := range []float32{40, 97, 200, 333} {
		lines := WrapLines(ts, s, w, wrapFont)
		if got := strings.Join(lines, ""); strings.ReplaceAll(got, " ", "") != strings.ReplaceAll(s, " ", "") {
			t.Fatalf("width %v lost text: %q", w, got)
		}
		for i, ln := range lines {
			if a := ts.Shape(ln, wrapFont).Advance; a > w && len([]rune(ln)) > 1 {
				t.Fatalf("width %v: line %d %q is %v wide", w, i, ln, a)
			}
			if i+1 < len(lines) && !strings.Contains(ln, "Wi") {
				next := strings.SplitN(lines[i+1], " ", 2)[0]
				if ts.Shape(ln+" "+next, wrapFont).Advance <= w {
					t.Fatalf("width %v: line %d %q had room for %q", w, i, ln, next)
				}
			}
		}
	}
}

func TestWrapLinesLongTokenIsFast(t *testing.T) {
	ts := bundled(t)
	for _, n := range []int{4000, 10000} {
		tok := strings.Repeat("QmFzZTY0IGJsb2Ig", n/16+1)[:n]
		start := time.Now()
		lines := WrapLines(ts, tok, 400, wrapFont)
		d := time.Since(start)
		t.Logf("%d chars: %v", n, d)
		if d > 50*time.Millisecond {
			t.Errorf("a %d-char token took %v to wrap", n, d)
		}
		if len(lines) < 2 || strings.Join(lines, "") != tok {
			t.Fatalf("%d-char token wrapped wrong: %d lines", n, len(lines))
		}
	}
}

func BenchmarkWrapLinesLongToken(b *testing.B) {
	ts := bundled(b)
	tok := strings.Repeat("QmFzZTY0IGJsb2Ig", 1000/16+1)[:1000]
	i := 0
	for b.Loop() {
		i++
		_ = WrapLines(ts, strconv.Itoa(i)+tok, 400, wrapFont) // a new text each time, so nothing is cached
	}
}
