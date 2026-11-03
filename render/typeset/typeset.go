// Package typeset shapes text into glyph runs and rasterises glyphs, independent of the backend.
package typeset

import (
	"iter"
	"unicode/utf8"
)

// Weight is a CSS font weight; 0 means Normal.
type Weight int

const (
	Normal   Weight = 400
	Medium   Weight = 500
	SemiBold Weight = 600
	Bold     Weight = 700
)

// Families of the bundled fonts.
const (
	Mono    = "JetBrains Mono"
	UI      = "Inter"
	Display = "Space Grotesk"
)

// Feature turns an OpenType feature on or off: {"liga", 0} turns ligatures off.
type Feature struct {
	Tag   string
	Value uint32
}

type FontSpec struct {
	Family   string
	Weight   Weight
	Italic   bool
	Size     float32 // logical pixels per em
	Tracking float32 // letter spacing, in em, after each cluster
	Features []Feature
}

type GlyphID uint32

// Glyph is one positioned glyph. X and Y are logical pixels from the run's pen; Y grows down.
type Glyph struct {
	Face *Face // nil from shapers without real fonts
	ID   GlyphID
	X, Y float32
	Rune int // index in the run's text, in runes, of the first rune of the glyph's cluster
}

// Run is a line of shaped text, in logical pixels.
type Run struct {
	Text            string // the source text, for accessibility and snapshots
	Font            FontSpec
	Glyphs          []Glyph
	Advance         float32
	Ascent, Descent float32 // both positive
}

// Carets yields the pen's place at each cluster boundary of the run, as a byte offset into Text and an x across it,
// from (0, 0) to (len(Text), Advance).
func (r *Run) Carets() iter.Seq2[int, float32] {
	return func(yield func(int, float32) bool) {
		if !yield(0, 0) {
			return
		}
		b, n := 0, 0 // a byte offset into Text and its index in runes
		for i, g := range r.Glyphs {
			if i > 0 && g.Rune == r.Glyphs[i-1].Rune {
				continue
			}
			for n < g.Rune && b < len(r.Text) {
				_, w := utf8.DecodeRuneInString(r.Text[b:])
				b, n = b+w, n+1
			}
			if b == 0 || b == len(r.Text) {
				continue
			}
			if !yield(b, g.X) {
				return
			}
		}
		if len(r.Text) > 0 {
			yield(len(r.Text), r.Advance)
		}
	}
}

// CaretX is the x across the run of the cluster boundary at or before byte offset off.
func (r *Run) CaretX(off int) float32 {
	x := float32(0)
	for b, cx := range r.Carets() {
		if b > off {
			break
		}
		x = cx
	}
	return x
}

type Shaper interface {
	Shape(text string, f FontSpec) *Run
}
