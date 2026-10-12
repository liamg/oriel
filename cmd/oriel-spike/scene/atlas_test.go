package scene

import (
	"image"
	"image/color"
	"testing"

	"gioui.org/font"
	"gioui.org/op"
	gtfont "github.com/go-text/typesetting/font"
)

func mask(w, h int) Coverage {
	m := image.NewAlpha(image.Rect(0, 0, w, h))
	for i := range m.Pix {
		m.Pix[i] = 255
	}
	return Coverage{Mask: m, Off: image.Pt(0, -h)}
}

func TestAtlasKeys(t *testing.T) {
	a := NewAtlas(64, 2)
	var g TextGamma
	base := glyphKey{coverageKey{face: 0, gid: 7, ppem: 13 * 64, sub: 0}, color.NRGBA{255, 255, 255, 255}}
	a.Add(base, mask(4, 4), &g)
	for _, k := range []glyphKey{
		{coverageKey{face: 1, gid: 7, ppem: 13 * 64}, base.col},
		{coverageKey{face: 0, gid: 8, ppem: 13 * 64}, base.col},
		{coverageKey{face: 0, gid: 7, ppem: 14 * 64}, base.col},
		{coverageKey{face: 0, gid: 7, ppem: 13 * 64, sub: 1}, base.col},
		{base.coverageKey, color.NRGBA{255, 0, 0, 255}},
	} {
		if _, ok := a.Lookup(k); ok {
			t.Errorf("%+v found, but only %+v was added", k, base)
		}
	}
	if _, ok := a.Lookup(base); !ok {
		t.Error("added glyph not found")
	}
}

func TestAtlasPacking(t *testing.T) {
	a := NewAtlas(64, 4)
	var g TextGamma
	var placed []AtlasEntry
	for i := range 60 {
		k := glyphKey{coverageKey{gid: gtfont.GID(i)}, color.NRGBA{A: 255}}
		e := a.Add(k, mask(3+i%7, 5+i%9), &g)
		if e.Page < 0 {
			t.Fatalf("glyph %d not placed", i)
		}
		if !e.Src.In(image.Rect(0, 0, 64, 64)) {
			t.Fatalf("glyph %d at %v, outside the page", i, e.Src)
		}
		placed = append(placed, e)
	}
	if a.Gen != 0 {
		t.Fatalf("atlas evicted (gen %d) with room to spare", a.Gen)
	}
	for i, p := range placed {
		for j, q := range placed[:i] {
			if p.Page == q.Page && p.Src.Overlaps(q.Src) {
				t.Errorf("glyphs %d %v and %d %v overlap on page %d", i, p.Src, j, q.Src, p.Page)
			}
		}
	}
}

func TestAtlasEviction(t *testing.T) {
	a := NewAtlas(16, 1)
	var g TextGamma
	k := func(i int) glyphKey { return glyphKey{coverageKey{gid: gtfont.GID(i)}, color.NRGBA{A: 255}} }
	a.Add(k(0), mask(10, 10), &g)
	old := a.Pages[0].Img
	before := append([]byte(nil), old.Pix...)
	a.Add(k(1), mask(10, 10), &g) // doesn't fit beside the first
	if a.Gen != 1 {
		t.Fatalf("gen = %d, want 1 after a full atlas", a.Gen)
	}
	if _, ok := a.Lookup(k(0)); ok {
		t.Error("evicted glyph still found")
	}
	if _, ok := a.Lookup(k(1)); !ok {
		t.Error("glyph added after eviction not found")
	}
	if a.Pages[0].Img == old || string(old.Pix) != string(before) {
		t.Error("eviction reused or changed a page the GPU may still be drawing from")
	}
	if e := a.Add(k(2), mask(20, 20), &g); e.Page >= 0 {
		t.Error("a glyph larger than a page was placed")
	}
}

func TestTextGamma(t *testing.T) {
	g := TextGamma{Darken: 0.25}
	for _, col := range []color.NRGBA{{230, 225, 210, 255}, {255, 210, 63, 255}, {20, 20, 24, 255}, {90, 60, 200, 255}} {
		if g.Alpha(0, col) != 0 || g.Alpha(255, col) != 255 {
			t.Errorf("%v: coverage 0 and 255 must map to themselves", col)
		}
		for c := 1; c < 256; c++ {
			if g.Alpha(uint8(c), col) < g.Alpha(uint8(c-1), col) {
				t.Errorf("%v: not monotonic at %d", col, c)
				break
			}
		}
	}
	// Light text over black already blends like sRGB in Gio's premultiplied-sRGB textures.
	white := color.NRGBA{255, 255, 255, 255}
	for _, c := range []uint8{32, 128, 200} {
		if got := g.Alpha(c, white); got < c-1 || got > c+1 {
			t.Errorf("white coverage %d -> %d, want unchanged", c, got)
		}
	}
	// Dark text is weighted up, so it doesn't thin out in linear blending.
	if g.Alpha(128, color.NRGBA{0, 0, 0, 255}) <= 128 {
		t.Error("dark text coverage 128 not darkened")
	}
}

func TestSubStep(t *testing.T) {
	for frac, want := range map[float32]uint8{0: 0, 0.1: 0, 0.13: 1, 0.25: 1, 0.5: 2, 0.74: 3, 0.9: 0} {
		if got := subStep(frac); got != want {
			t.Errorf("subStep(%v) = %d, want %d", frac, got, want)
		}
	}
}

func TestShapedText(t *testing.T) {
	ts, err := newTypesetter(newAtlasOps(), nil)
	if err != nil {
		t.Fatal(err)
	}
	fi := ts.face(font.Font{Typeface: Mono})
	if fi < 0 {
		t.Fatal("no face for JetBrains Mono")
	}
	plain := ts.shape(shapeKey{face: fi, ppem: 13 * 64, text: "MMMM"})
	tracked := ts.shape(shapeKey{face: fi, ppem: 13 * 64, text: "MMMM", track: 2 * 64})
	if got, want := tracked.advance-plain.advance, 4*2*64; int(got) != want {
		t.Errorf("tracking added %v, want %v (one step per cluster)", got, want)
	}
	if plain.glyphs[1].x-plain.glyphs[0].x != plain.glyphs[2].x-plain.glyphs[1].x {
		t.Error("monospace advances differ")
	}
	gid, _ := ts.faces[fi].NominalGlyph('H')
	cov := ts.coverage(coverageKey{face: fi, gid: gid, ppem: 13 * 64})
	if cov.Mask == nil || cov.Off.Y >= 0 || cov.Mask.Bounds().Dy() < 8 {
		t.Errorf("H at 13 px: mask %v off %v; want a glyph above the baseline", cov.Mask.Bounds(), cov.Off)
	}
	space, _ := ts.faces[fi].NominalGlyph(' ')
	if c := ts.coverage(coverageKey{face: fi, gid: space, ppem: 13 * 64}); c.Mask != nil {
		t.Error("space has a mask")
	}
}

func TestTextModesMeasureAlike(t *testing.T) {
	sh, err := NewFonts()
	if err != nil {
		t.Fatal(err)
	}
	sh.NoFallback = true
	var ops op.Ops
	paths := NewCanvas(&ops, sh, 1)
	atlas := NewCanvas(&ops, sh, 1)
	atlas.Mode = TextAtlas
	for _, s := range []string{"func roundHalfEven(v decimal.Decimal) {", "pricing-rounding"} {
		for _, st := range []TextStyle{codeStyle, {Face: UI, Size: 13}} {
			p, a := paths.Measure(s, st), atlas.Measure(s, st)
			if d := p - a; d < -0.5 || d > 0.5 {
				t.Errorf("%q in %s: paths %.2f px, atlas %.2f px", s, st.Face, p, a)
			}
		}
	}
}
