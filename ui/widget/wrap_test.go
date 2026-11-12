package widget

import (
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
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

// A token too long for a line is cut across lines, none of it lost.
func TestWrapLinesLongToken(t *testing.T) {
	ts := bundled(t)
	for _, n := range []int{4000, 10000} {
		tok := strings.Repeat("QmFzZTY0IGJsb2Ig", n/16+1)[:n]
		if lines := WrapLines(ts, tok, 400, wrapFont); len(lines) < 2 || strings.Join(lines, "") != tok {
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

func TestCodeLinesKeepSpacesAndBreakAfterOne(t *testing.T) {
	sh := headless.Shaper{} // 10px a rune
	f := typeset.FontSpec{Size: 10 / 0.6}
	got := codeLines(sh, "  ab cd ef\n\n\tabcdefgh", 60, f)
	want := []wrapLine{{"  ab ", false}, {"cd ef", true}, {"", false}, {"    ab", false}, {"cdefgh", true}}
	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	}
}

// A long line of code wraps to the box, and copied it's the line it was: no newline where it wrapped.
func TestWrapCodeCopiesAsWritten(t *testing.T) {
	src := `cd ~/Library/Logs/Oriel && grep -hE "agent stopped|didn't start|sandbox restart" oriel.log` + "\n" + "  ls"
	clip := &testClip{}
	lines := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Px(200), Selectable: true}, func(c *ui.Ctx) {
			lines = WrapCode(c, "code", src, 200, 0, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12}})
		})
	})
	h.u.SetClipboard(clip)
	if lines < 4 {
		t.Fatalf("%d lines: the long line didn't wrap", lines)
	}
	h.pointer(render.PointerPress, render.ButtonPrimary, 1, 5)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, 199, 399)
	h.frame()
	h.pointer(render.PointerRelease, 0, 199, 399)
	h.frame()
	if got := h.u.SelectedText(); got != src {
		t.Errorf("copied %q, want %q", got, src)
	}
}

// A paragraph Rich wraps copies as it was written: its words, not its lines.
func TestRichCopiesAsWritten(t *testing.T) {
	src := "The quick brown fox jumps over the lazy dog, then runs on past the barn and over the hill."
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Px(120), Selectable: true}, func(c *ui.Ctx) {
			if n := Rich(c, "p", []Span{{Text: "The quick brown fox "}, {Text: "jumps", Code: true}, {Text: " over the lazy dog, then runs on past the barn and over the hill."}}, 120, ui.TextStyle{Font: wrapFont}, RichOpts{}); n < 3 {
				t.Fatalf("%d lines: it didn't wrap", n)
			}
		})
	})
	h.pointer(render.PointerPress, render.ButtonPrimary, 1, 5)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, 599, 399)
	h.frame()
	if got := h.u.SelectedText(); got != src {
		t.Errorf("copied %q, want %q", got, src)
	}
}

// A code span or a word wider than the whole line breaks onto as many lines as it needs, none wider than the width
// and none cut short, as WrapCode breaks code; it copies as it was written.
func TestRichBreaksWhatsTooWide(t *testing.T) {
	long := "RoundingModeHalfEven(" + strings.Repeat("scaled, ", 6) + "places)"
	word := "https://example.com/" + strings.Repeat("abcdefgh", 6)
	src := "Call " + long + " or see " + word + " now."
	var id ui.ID
	var lines int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Px(160), Selectable: true}, func(c *ui.Ctx) {
			id = c.Next("p")
			lines = Rich(c, "p", []Span{{Text: "Call "}, {Text: long, Code: true}, {Text: " or see " + word + " now."}}, 160, ui.TextStyle{Font: wrapFont}, RichOpts{})
		})
	})
	h.frame()
	if lines < 5 {
		t.Fatalf("%d lines at 160px", lines)
	}
	box := h.rect(id)
	if box.Dx() > 160 {
		t.Errorf("the paragraph is %v wide, over its 160", box.Dx())
	}
	var drawn strings.Builder
	for _, r := range h.list.Runs() {
		if r.Box.Max.X > box.Min.X+160 || strings.HasSuffix(r.Text, "…") {
			t.Errorf("%q runs past the line: %v", r.Text, r.Box)
		}
		drawn.WriteString(r.Text)
	}
	if got := strings.ReplaceAll(drawn.String(), " ", ""); got != strings.ReplaceAll(src, " ", "") {
		t.Errorf("drew %q, want all of %q", got, src)
	}
	h.pointer(render.PointerPress, render.ButtonPrimary, 1, 5)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, 599, 399)
	h.frame()
	if got := h.u.SelectedText(); got != src {
		t.Errorf("copied %q, want %q", got, src)
	}
}
