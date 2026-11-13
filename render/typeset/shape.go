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

// maxRuns caps each generation of the shaped-run cache: once the current one is full it becomes the previous, and
// a run found there moves back to the current.
const maxRuns = 8192

// Typesetter shapes with the bundled faces and falls back to system faces, loaded off the frame path.
// It's safe for concurrent use.
type Typesetter struct {
	mu      sync.Mutex
	bundled []*Face
	extra   []*Face // fallback faces that have landed
	fb      *fallbacks
	shaper  shaping.HarfbuzzShaper
	runs    map[runKey]*Run // the current generation
	old     map[runKey]*Run // the previous one

	installed families
	used      map[string][]*Face // by role: the installed family each draws with, when not the bundled one
	gen       uint64
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

// Face returns the face that f resolves to.
func (t *Typesetter) Face(f FontSpec) *Face {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.face(f)
}

func (t *Typesetter) Shape(text string, f FontSpec) *Run {
	t.mu.Lock()
	defer t.mu.Unlock()
	if landed := t.fb.take(); len(landed) > 0 {
		t.extra = append(t.extra, landed...)
		t.runs, t.old = map[runKey]*Run{}, nil // runs with tofu can now show the rune
	}
	k := runKey{text, f.Family, f.Weight, f.Italic, f.Size, f.Tracking, featureKey(f.Features)}
	if r, ok := t.runs[k]; ok {
		return r
	}
	r, ok := t.old[k]
	if !ok {
		r = t.shape(text, f)
	}
	if len(t.runs) >= maxRuns {
		t.runs, t.old = map[runKey]*Run{}, t.runs
	}
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
	face := t.face(f)
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
			// A sequence stays in its first rune's face, so the face can join it into one glyph.
			if inSequence(runes[i]) || runes[i-1] == zwj && cur.Has(runes[i]) {
				next = cur
			}
			if next == cur {
				continue
			}
		}
		t.shapeSegment(run, runes, start, i, cur, f)
		start, cur = i, next
	}
	return run
}

const zwj = 0x200D

// inSequence is a rune that only modifies or joins the one before it: a variation selector, the zero width joiner,
// the keycap, a skin tone, or a tag (a subdivision flag's).
func inSequence(r rune) bool {
	return r == 0xFE0E || r == 0xFE0F || r == zwj || r == 0x20E3 || r >= 0x1F3FB && r <= 0x1F3FF || r >= 0xE0020 && r <= 0xE007F
}

// asEmoji is runes[i] to be drawn as an emoji: an emoji by default (Emoji_Presentation) not asked to look like text,
// or one followed by the emoji presentation selector, a keycap or a skin tone.
func asEmoji(runes []rune, i int) bool {
	var n rune
	if i+1 < len(runes) {
		n = runes[i+1]
	}
	return defaultEmoji(runes[i]) && n != 0xFE0E || n == 0xFE0F || n == 0x20E3 || n >= 0x1F3FB && n <= 0x1F3FF
}

// faceFor is the face for runes[i]: a bundled emoji face for one asked to look like an emoji; else primary if it
// has it, else another bundled face that does (the emoji faces last), else a system fallback that does, else primary
// (as tofu) after asking for a fallback.
func (t *Typesetter) faceFor(primary *Face, runes []rune, i int) *Face {
	r := runes[i]
	if asEmoji(runes, i) {
		for _, fam := range []string{Emoji, EmojiMono} {
			for _, fc := range t.bundled {
				if fc.Family == fam && fc.Has(r) {
					return fc
				}
			}
		}
	}
	if primary.Has(r) || r < 0x20 {
		return primary
	}
	// A bundled face of the primary's style first, then any (the emoji and symbol faces have no italic), then the
	// emoji faces, whose symbols are drawn as emoji rather than in the text's weight: colour, then monochrome.
	text := func(fc *Face) bool { return fc.Family != Emoji && fc.Family != EmojiMono }
	for _, pass := range []func(*Face) bool{
		func(fc *Face) bool { return text(fc) && fc.Italic == primary.Italic },
		text,
		func(fc *Face) bool { return fc.Family == Emoji },
		func(fc *Face) bool { return fc.Family == EmojiMono },
	} {
		for _, fc := range t.bundled {
			if pass(fc) && fc.Has(r) {
				return fc
			}
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
		run.Glyphs = append(run.Glyphs, Glyph{Face: face, ID: GlyphID(g.GlyphID), X: run.Advance + fx(g.XOffset), Y: -fx(g.YOffset), Rune: g.ClusterIndex})
		run.Advance += fx(g.XAdvance)
		// Tracking goes after each cluster, not inside it.
		if i == len(out.Glyphs)-1 || out.Glyphs[i+1].ClusterIndex != g.ClusterIndex {
			run.Advance += track
		}
	}
}

func fx(v fixed.Int26_6) float32 { return float32(v) / 64 }
