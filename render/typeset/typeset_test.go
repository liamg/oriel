package typeset

import (
	"image"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"
)

func newTS(t *testing.T, onLoad func()) *Typesetter {
	t.Helper()
	ts, err := New(onLoad)
	if err != nil {
		t.Fatal(err)
	}
	ts.fb.find = func(rune) []string { return nil }
	return ts
}

func TestBundledFaces(t *testing.T) {
	faces, err := Bundled()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range faces {
		got = append(got, f.Family)
	}
	for _, fam := range []string{Mono, UI, Display} {
		if !slices.Contains(got, fam) {
			t.Errorf("no bundled %s face among %q", fam, got)
		}
	}
}

func TestMatch(t *testing.T) {
	ts := newTS(t, nil)
	for _, tt := range []struct {
		spec   FontSpec
		family string
		weight Weight
		italic bool
	}{
		{FontSpec{Family: Mono}, Mono, Normal, false},
		{FontSpec{Family: Mono, Weight: Bold}, Mono, Bold, false},
		{FontSpec{Family: Mono, Italic: true}, Mono, Normal, true},
		{FontSpec{Family: UI, Weight: SemiBold}, UI, SemiBold, false},
		{FontSpec{Family: UI, Weight: 900}, UI, SemiBold, false},
		{FontSpec{Family: "Comic Sans"}, UI, Normal, false},
	} {
		f := ts.Face(tt.spec)
		if f == nil || f.Family != tt.family || f.Weight != tt.weight || f.Italic != tt.italic {
			t.Errorf("Face(%+v) = %+v", tt.spec, f)
		}
	}
}

func TestShapeMonospace(t *testing.T) {
	ts := newTS(t, nil)
	r := ts.Shape("func", FontSpec{Family: Mono, Size: 10})
	if len(r.Glyphs) != 4 {
		t.Fatalf("%d glyphs", len(r.Glyphs))
	}
	adv := r.Glyphs[1].X - r.Glyphs[0].X
	for i, g := range r.Glyphs {
		if math.Abs(float64(g.X-float32(i)*adv)) > 1e-3 {
			t.Errorf("glyph %d at %v, want %v", i, g.X, float32(i)*adv)
		}
	}
	if math.Abs(float64(r.Advance-4*adv)) > 1e-3 {
		t.Errorf("advance %v, want %v", r.Advance, 4*adv)
	}
	if r.Ascent <= 0 || r.Descent <= 0 || r.Ascent+r.Descent < 10 {
		t.Errorf("ascent %v descent %v at 10px", r.Ascent, r.Descent)
	}
	if ts.Shape("func", FontSpec{Family: Mono, Size: 10}) != r {
		t.Error("the same text and font shaped twice")
	}
	tracked := ts.Shape("func", FontSpec{Family: Mono, Size: 10, Tracking: 0.1})
	if math.Abs(float64(tracked.Advance-(r.Advance+4))) > 1e-3 {
		t.Errorf("tracked advance %v, want %v", tracked.Advance, r.Advance+4)
	}
}

func TestFeatures(t *testing.T) {
	ts := newTS(t, nil)
	ids := func(r *Run) []GlyphID {
		var out []GlyphID
		for _, g := range r.Glyphs {
			out = append(out, g.ID)
		}
		return out
	}
	on := ts.Shape("===", FontSpec{Family: Mono, Size: 13})
	off := ts.Shape("===", FontSpec{Family: Mono, Size: 13, Features: []Feature{{"calt", 0}}})
	if slices.Equal(ids(on), ids(off)) {
		t.Errorf("calt off shapes === the same as calt on: %v", ids(on))
	}
}

func TestRasterise(t *testing.T) {
	ts := newTS(t, nil)
	r := ts.Shape("H ", FontSpec{Family: Mono, Size: 20})
	h := Rasterise(r.Glyphs[0].Face, r.Glyphs[0].ID, 20, 0)
	if h.Mask == nil || h.Mask.Bounds().Dy() < 12 || h.Off.Y >= 0 {
		t.Fatalf("H coverage %v off %v", h.Mask.Bounds(), h.Off)
	}
	if sp := Rasterise(r.Glyphs[1].Face, r.Glyphs[1].ID, 20, 0); sp.Mask != nil {
		t.Error("space has coverage")
	}
	shifted := Rasterise(r.Glyphs[0].Face, r.Glyphs[0].ID, 20, 2)
	if slices.Equal(h.Mask.Pix, shifted.Mask.Pix) {
		t.Error("a half-pixel shift rasterised the same")
	}
	for _, tt := range []struct {
		x    float32
		ix   int
		step uint8
	}{{3, 3, 0}, {3.24, 3, 1}, {3.5, 3, 2}, {3.9, 4, 0}, {-0.25, -1, 3}} {
		if ix, s := SubStep(tt.x); ix != tt.ix || s != tt.step {
			t.Errorf("SubStep(%v) = %d, %d; want %d, %d", tt.x, ix, s, tt.ix, tt.step)
		}
	}
}

// interOnly finds a rune Inter has and JetBrains Mono lacks.
func interOnly(t *testing.T, ts *Typesetter) rune {
	mono, inter := ts.Face(FontSpec{Family: Mono}), ts.Face(FontSpec{Family: UI})
	for _, r := range "ẞŊŦǄ₿ꞋḀ" {
		if !mono.Has(r) && inter.Has(r) {
			return r
		}
	}
	t.Skip("no rune in Inter but not JetBrains Mono among the candidates")
	return 0
}

const interFile = "fonts/inter/Inter-Regular.ttf"

func TestLoadFaceMapsAndPicksTheCoveringFace(t *testing.T) {
	ts := newTS(t, nil)
	r := interOnly(t, ts)
	face, i := loadFace(interFile, r, map[string]bool{})
	if face == nil || i != 0 || !face.Has(r) {
		t.Fatalf("loadFace(%q) = %v, %d", r, face, i)
	}
	if face, _ := loadFace(interFile, r, map[string]bool{interFile + "#0": true}); face != nil {
		t.Error("loaded a face already tried")
	}
	if face, _ := loadFace(interFile, 0x10FFFD, map[string]bool{}); face != nil {
		t.Error("loaded a face for a rune it doesn't have")
	}
}

func TestFallbackLandsOffTheFramePath(t *testing.T) {
	loaded := make(chan struct{}, 1)
	ts := newTS(t, func() { loaded <- struct{}{} })
	r := interOnly(t, ts)
	mono := ts.Face(FontSpec{Family: Mono})
	ts.bundled = []*Face{mono} // Inter stands in for a system font
	var asked []rune
	ts.fb.find = func(r rune) []string { asked = append(asked, r); return []string{interFile} }

	spec := FontSpec{Family: Mono, Size: 13}
	first := ts.Shape("a"+string(r), spec)
	if first.Glyphs[1].Face != mono {
		t.Fatalf("before the fallback lands, %q is drawn by %s", r, first.Glyphs[1].Face.Name)
	}
	select {
	case <-loaded:
	case <-time.After(5 * time.Second):
		t.Fatal("no fallback landed")
	}
	again := ts.Shape("a"+string(r), spec)
	if again.Glyphs[0].Face != mono || again.Glyphs[1].Face.Family != UI {
		t.Errorf("after it lands: %s, %s", again.Glyphs[0].Face.Name, again.Glyphs[1].Face.Name)
	}
	ts.Shape(string(r)+"b", spec)
	if names, _ := ts.Fallbacks(); len(names) != 1 || len(asked) != 1 {
		t.Errorf("loaded %q after asking for %q; want one face for one rune", names, asked)
	}
}

func TestRunCacheIsBounded(t *testing.T) {
	ts := newTS(t, nil)
	for i := range 3*maxRuns + 10 {
		ts.Shape(string(rune('a'+i%26))+string(rune(i)), FontSpec{Family: Mono, Size: 13})
	}
	if n := len(ts.runs) + len(ts.old); n > 2*maxRuns {
		t.Errorf("%d runs cached, cap %d", n, 2*maxRuns)
	}
}

// Filling the cache keeps the runs shaped just before: a frame that shapes more runs than a generation holds still
// finds the ones it drew last.
func TestRunCacheKeepsRecentRunsWhenFull(t *testing.T) {
	ts := newTS(t, nil)
	spec := FontSpec{Family: Mono, Size: 13}
	key := func(i int) string { return "run " + strconv.Itoa(i) }
	recent := map[string]*Run{}
	for i := range maxRuns {
		r := ts.Shape(key(i), spec)
		if i >= maxRuns-100 {
			recent[key(i)] = r
		}
	}
	ts.Shape("one past full", spec)
	for k, r := range recent {
		if ts.Shape(k, spec) != r {
			t.Fatalf("%q was shaped again after the cache filled", k)
		}
	}
}

func fill(dst *image.RGBA, at image.Point) {}

func TestAtlasPacking(t *testing.T) {
	a := NewAtlas(64, 4)
	var placed []Entry
	for i := range 60 {
		e := a.Add(i, image.Pt(3+i%7, 5+i%9), image.Point{}, fill)
		if e.Page < 0 || !e.Src.In(image.Rect(0, 0, 64, 64)) {
			t.Fatalf("entry %d: %+v", i, e)
		}
		placed = append(placed, e)
	}
	if a.Gen != 0 {
		t.Fatalf("atlas started again (gen %d) with room to spare", a.Gen)
	}
	for i, p := range placed {
		for j, q := range placed[:i] {
			if p.Page == q.Page && p.Src.Overlaps(q.Src) {
				t.Errorf("%d %v and %d %v overlap on page %d", i, p.Src, j, q.Src, p.Page)
			}
		}
	}
	if e, ok := a.Lookup(7); !ok || e != placed[7] {
		t.Error("lookup lost an entry")
	}
}

func TestAtlasStartsAgainWhenFull(t *testing.T) {
	a := NewAtlas(16, 1)
	a.Add(0, image.Pt(10, 10), image.Point{}, fill)
	old := a.Pages[0]
	a.Add(1, image.Pt(10, 10), image.Point{}, fill)
	if a.Gen != 1 || a.Pages[0] == old {
		t.Fatalf("gen %d; a full atlas must start on new pages", a.Gen)
	}
	if _, ok := a.Lookup(0); ok {
		t.Error("an entry survived the reset")
	}
	if e := a.Add(2, image.Pt(40, 40), image.Point{}, fill); e.Page != -1 {
		t.Errorf("an image bigger than a page was placed: %+v", e)
	}
}

func TestOtherBundledFacesFillGaps(t *testing.T) {
	ts := newTS(t, nil)
	r := interOnly(t, ts)
	run := ts.Shape("a"+string(r), FontSpec{Family: Mono, Size: 13})
	if run.Glyphs[0].Face.Family != Mono || run.Glyphs[1].Face.Family != UI {
		t.Errorf("%q drawn by %s, %s", "a"+string(r), run.Glyphs[0].Face.Name, run.Glyphs[1].Face.Name)
	}
}

func TestShapeEmpty(t *testing.T) {
	r := newTS(t, nil).Shape("", FontSpec{Family: UI, Size: 12})
	if len(r.Glyphs) != 0 || r.Advance != 0 || r.Ascent <= 0 {
		t.Errorf("empty run %+v: no glyphs, no advance, but the font's ascent", r)
	}
}
