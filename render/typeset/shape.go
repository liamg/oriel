package typeset

import (
	"strconv"
	"strings"
	"sync"

	"github.com/go-text/typesetting/di"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
)

// maxRuns caps the shaped-run cache; past it the cache starts again.
const maxRuns = 8192

// Typesetter shapes with the bundled faces and falls back to system faces, loaded off the frame path.
// It's safe for concurrent use.
type Typesetter struct {
	mu      sync.Mutex
	bundled []*Face
	extra   []*Face // fallback faces that have landed
	fb      *fallbacks
	shaper  shaping.HarfbuzzShaper
	runs    map[runKey]*Run
}

type runKey struct {
	text     string
	family   string
	weight   Weight
	italic   bool
	size     float32
	tracking float32
	features string
}

// New returns a typesetter over the bundled fonts. onLoad, if set, is called from another goroutine
// whenever a fallback face lands, so the caller can redraw: runs shaped since then show the rune.
func New(onLoad func()) (*Typesetter, error) {
	faces, err := Bundled()
	if err != nil {
		return nil, err
	}
	return &Typesetter{bundled: faces, fb: newFallbacks(onLoad), runs: map[runKey]*Run{}}, nil
}

// NewBundled returns a typesetter with the bundled faces only: no system fallback, so a render is the same on
// every machine. Runes the bundled faces lack draw as tofu.
func NewBundled() (*Typesetter, error) {
	t, err := New(nil)
	if err != nil {
		return nil, err
	}
	t.fb.find = func(rune) []string { return nil }
	return t, nil
}

// Close stops loading fallback faces. The typesetter still shapes with what it has.
func (t *Typesetter) Close() { t.fb.close() }

// Fallbacks lists the system faces loaded so far, and the runes no face covered.
func (t *Typesetter) Fallbacks() (names []string, missing []rune) { return t.fb.list() }

// Face returns the bundled face that f resolves to.
func (t *Typesetter) Face(f FontSpec) *Face { return match(t.bundled, f) }

func (t *Typesetter) Shape(text string, f FontSpec) *Run {
	t.mu.Lock()
	defer t.mu.Unlock()
	if landed := t.fb.take(); len(landed) > 0 {
		t.extra = append(t.extra, landed...)
		t.runs = map[runKey]*Run{} // runs with tofu can now show the rune
	}
	k := runKey{text, f.Family, f.Weight, f.Italic, f.Size, f.Tracking, featureKey(f.Features)}
	if r, ok := t.runs[k]; ok {
		return r
	}
	if len(t.runs) >= maxRuns {
		t.runs = map[runKey]*Run{}
	}
	r := t.shape(text, f)
	t.runs[k] = r
	return r
}

func featureKey(fs []Feature) string {
	var b strings.Builder
	for _, f := range fs {
		b.WriteString(f.Tag + "=" + strconv.Itoa(int(f.Value)) + ";")
	}
	return b.String()
}

func (t *Typesetter) shape(text string, f FontSpec) *Run {
	run := &Run{Text: text, Font: f}
	face := match(t.bundled, f)
	if face == nil {
		return run
	}
	if ext, ok := face.face.FontHExtents(); ok {
		scale := f.Size / float32(face.Upem())
		run.Ascent, run.Descent = ext.Ascender*scale, -ext.Descender*scale
	}
	runes := []rune(text)
	if len(runes) == 0 {
		return run
	}
	start, cur := 0, t.faceFor(face, runes, 0)
	for i := 1; i <= len(runes); i++ {
		var next *Face
		if i < len(runes) {
			next = t.faceFor(face, runes, i)
			if next == cur {
				continue
			}
		}
		t.shapeSegment(run, runes, start, i, cur, f)
		start, cur = i, next
	}
	return run
}

// faceFor is primary if it has runes[i], else another bundled face that does, else a system fallback
// that does, else primary (as tofu) after asking for a fallback.
func (t *Typesetter) faceFor(primary *Face, runes []rune, i int) *Face {
	r := runes[i]
	if primary.Has(r) || r < 0x20 {
		return primary
	}
	for _, fc := range t.bundled {
		if fc.Italic == primary.Italic && fc.Has(r) {
			return fc
		}
	}
	for _, fc := range t.extra {
		if fc.Has(r) {
			return fc
		}
	}
	t.fb.request(r)
	return primary
}

func (t *Typesetter) shapeSegment(run *Run, runes []rune, start, end int, face *Face, f FontSpec) {
	in := shaping.Input{
		Text:      runes,
		RunStart:  start,
		RunEnd:    end,
		Direction: di.DirectionLTR,
		Face:      face.face,
		Size:      fixed.Int26_6(f.Size * 64),
		Script:    language.Latin,
		Language:  language.NewLanguage("en"),
	}
	for _, ft := range f.Features {
		if len(ft.Tag) == 4 {
			in.FontFeatures = append(in.FontFeatures, shaping.FontFeature{Tag: ot.MustNewTag(ft.Tag), Value: ft.Value})
		}
	}
	out := t.shaper.Shape(in)
	track := f.Tracking * f.Size
	for i, g := range out.Glyphs {
		run.Glyphs = append(run.Glyphs, Glyph{Face: face, ID: GlyphID(g.GlyphID), X: run.Advance + fx(g.XOffset), Y: -fx(g.YOffset)})
		run.Advance += fx(g.XAdvance)
		// Tracking goes after each cluster, not inside it.
		if i == len(out.Glyphs)-1 || out.Glyphs[i+1].ClusterIndex != g.ClusterIndex {
			run.Advance += track
		}
	}
}

func fx(v fixed.Int26_6) float32 { return float32(v) / 64 }
