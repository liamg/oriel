package typeset

import (
	"strings"
	"testing"
)

// fixedShaper gives every rune an advance of 7 and counts the runes it shapes.
type fixedShaper struct{ shaped int }

func (f *fixedShaper) Shape(s string, font FontSpec) *Run {
	r := &Run{Text: s, Font: font}
	for i := range []rune(s) {
		r.Glyphs = append(r.Glyphs, Glyph{X: float32(i) * 7, Rune: i})
		r.Advance += 7
	}
	f.shaped += len(r.Glyphs)
	return r
}

func TestElide(t *testing.T) {
	font := FontSpec{Family: UI, Size: 13}
	for _, tt := range []struct {
		s     string
		width float32
		want  string
	}{
		{"short", 100, "short"},
		{"exactly", 49, "exactly"},
		{"a longer label", 49, "a long…"},
		{"abc", 6, ""},
		{"abc", 7, "…"},
		{"abcdef", 14, "a…"},
	} {
		if got := Elide(&fixedShaper{}, tt.s, font, tt.width); got != tt.want {
			t.Errorf("Elide(%q, %v) = %q, want %q", tt.s, tt.width, got, tt.want)
		}
	}
}

// A long text is shaped only as far as it can reach the width.
func TestElideShapesWhatFits(t *testing.T) {
	sh := &fixedShaper{}
	got := Elide(sh, strings.Repeat("x", 10_000), FontSpec{Family: UI, Size: 13}, 700)
	if want := strings.Repeat("x", 99) + "…"; got != want {
		t.Errorf("got %d runes, want %d", len([]rune(got)), len([]rune(want)))
	}
	if sh.shaped > 2000 {
		t.Errorf("shaped %d runes of 10,000 to cut them at 100", sh.shaped)
	}
}
