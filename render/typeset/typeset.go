// Package typeset shapes text into glyph runs, independent of the backend.
package typeset

type FontSpec struct {
	Family string
	Size   float32
}

type GlyphID uint32

type Glyph struct {
	ID GlyphID
	X  float32 // offset from the run's origin
}

// Run is a line of shaped text.
type Run struct {
	Text   string // the source text, for accessibility and snapshots
	Font   FontSpec
	Glyphs []Glyph
	Width  float32
	Height float32
}

type Shaper interface {
	Shape(text string, f FontSpec) *Run
}
