// Package typeset shapes text into glyph runs and rasterises glyphs, independent of the backend.
package typeset

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

type Shaper interface {
	Shape(text string, f FontSpec) *Run
}
