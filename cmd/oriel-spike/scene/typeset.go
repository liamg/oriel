package scene

import (
	"fmt"
	"image"
	"image/color"
	"math"

	giofont "gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"github.com/go-text/typesetting/di"
	"github.com/go-text/typesetting/font"
	"github.com/go-text/typesetting/language"
	"github.com/go-text/typesetting/shaping"
	"golang.org/x/image/math/fixed"
)

// TextMode picks how the canvas draws text.
type TextMode int

const (
	// TextPaths fills every glyph's outline through Gio each frame.
	TextPaths TextMode = iota
	// TextAtlas shapes with go-text and draws each glyph as a quad from a tinted glyph atlas.
	TextAtlas
	// TextRuns composes each shaped run into its own cached image and draws it as one quad.
	TextRuns
)

// ParseTextMode reads "paths", "atlas" or "runs".
func ParseTextMode(s string) (TextMode, error) {
	switch s {
	case "paths":
		return TextPaths, nil
	case "atlas":
		return TextAtlas, nil
	case "runs":
		return TextRuns, nil
	}
	return 0, fmt.Errorf("unknown text mode %q (paths, atlas, runs)", s)
}

func (m TextMode) String() string { return [...]string{"paths", "atlas", "runs"}[m] }

// typesetter shapes and rasterises text for the atlas and runs modes, and keeps its caches across frames.
type typesetter struct {
	faces   []*font.Face
	fonts   []giofont.Font
	shaper  shaping.HarfbuzzShaper
	shaped  map[shapeKey]*shapedRun
	covs    map[coverageKey]Coverage
	gamma   TextGamma
	atlas   *Atlas
	pageOps []pageOp
	runs    map[runKey]*runImage
	frame   int
}

type pageOp struct {
	op      paint.ImageOp
	gen     int
	version int
	frame   int
	valid   bool
}

type shapeKey struct {
	face    int
	ppem    fixed.Int26_6
	text    string
	track   fixed.Int26_6
	feature string
}

type placedGlyph struct {
	gid font.GID
	x   fixed.Int26_6 // pen x relative to the run start
	y   fixed.Int26_6 // offset down from the baseline
}

type shapedRun struct {
	glyphs  []placedGlyph
	advance fixed.Int26_6
}

type runKey struct {
	shapeKey
	col color.NRGBA
	sub uint8
}

type runImage struct {
	op   paint.ImageOp
	off  image.Point // from the pen to the image origin
	used int
}

func newTypesetter() (*typesetter, error) {
	coll, err := Collection()
	if err != nil {
		return nil, err
	}
	ts := &typesetter{
		shaped: map[shapeKey]*shapedRun{},
		covs:   map[coverageKey]Coverage{},
		gamma:  TextGamma{Darken: 0.25},
		atlas:  NewAtlas(1024, 4),
		runs:   map[runKey]*runImage{},
	}
	for _, f := range coll {
		of, ok := f.Face.(opentype.Face)
		if !ok {
			continue
		}
		ts.faces = append(ts.faces, of.Face())
		ts.fonts = append(ts.fonts, f.Font)
	}
	return ts, nil
}

// beginFrame ages the per-frame caches.
func (ts *typesetter) beginFrame() {
	for i := range ts.pageOps {
		p := &ts.pageOps[i]
		// An op first used last frame was uploaded with everything added during that frame.
		if p.valid && p.frame == ts.frame && p.gen == ts.atlas.Gen && i < len(ts.atlas.Pages) {
			p.version = ts.atlas.Pages[i].Version
		}
	}
	for k, r := range ts.runs {
		if ts.frame-r.used > 2 {
			delete(ts.runs, k)
		}
	}
	if len(ts.shaped) > 8192 {
		ts.shaped = map[shapeKey]*shapedRun{}
	}
	if len(ts.covs) > 8192 {
		ts.covs = map[coverageKey]Coverage{}
	}
	ts.frame++
}

// face finds the bundled face for f: same typeface, same style if there is one, nearest weight.
func (ts *typesetter) face(f giofont.Font) int {
	best, bestScore := -1, math.MaxInt
	for i, g := range ts.fonts {
		if g.Typeface != f.Typeface {
			continue
		}
		score := abs(int(g.Weight) - int(f.Weight))
		if g.Style != f.Style {
			score += 10_000
		}
		if score < bestScore {
			best, bestScore = i, score
		}
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// supported reports whether face i has a glyph for r.
func (ts *typesetter) supported(i int, r rune) bool {
	_, ok := ts.faces[i].NominalGlyph(r)
	return ok
}

func (ts *typesetter) shape(k shapeKey) *shapedRun {
	if r, ok := ts.shaped[k]; ok {
		return r
	}
	runes := []rune(k.text)
	out := ts.shaper.Shape(shaping.Input{
		Text:      runes,
		RunEnd:    len(runes),
		Direction: di.DirectionLTR,
		Face:      ts.faces[k.face],
		Size:      k.ppem,
		Script:    language.Latin,
		Language:  language.NewLanguage("en"),
	})
	run := &shapedRun{glyphs: make([]placedGlyph, 0, len(out.Glyphs))}
	var x fixed.Int26_6
	for i, g := range out.Glyphs {
		run.glyphs = append(run.glyphs, placedGlyph{gid: g.GlyphID, x: x + g.XOffset, y: -g.YOffset})
		x += g.XAdvance
		// Tracking goes after each cluster, not inside it.
		if i == len(out.Glyphs)-1 || out.Glyphs[i+1].ClusterIndex != g.ClusterIndex {
			x += k.track
		}
	}
	run.advance = x
	ts.shaped[k] = run
	return run
}

func (ts *typesetter) coverage(k coverageKey) Coverage {
	if c, ok := ts.covs[k]; ok {
		return c
	}
	var cov Coverage
	if o, ok := ts.faces[k.face].GlyphDataOutline(k.gid); ok {
		scale := float32(k.ppem) / 64 / float32(ts.faces[k.face].Upem())
		cov = rasterise(o.Segments, scale, float32(k.sub)/SubpixelSteps)
	}
	ts.covs[k] = cov
	return cov
}

func subStep(frac float32) uint8 {
	return uint8(min(SubpixelSteps-1, int(frac*SubpixelSteps+0.5)%SubpixelSteps))
}

// pageImageOp returns the ImageOp for an atlas page, replacing it when the page has changed
// since the op was uploaded.
func (ts *typesetter) pageImageOp(i int) paint.ImageOp {
	for len(ts.pageOps) <= i {
		ts.pageOps = append(ts.pageOps, pageOp{})
	}
	p := &ts.pageOps[i]
	pg := ts.atlas.Pages[i]
	stale := !p.valid || p.gen != ts.atlas.Gen || (p.version != pg.Version && p.frame != ts.frame)
	if stale {
		op := paint.NewImageOp(pg.Img)
		op.Filter = paint.FilterNearest
		*p = pageOp{op: op, gen: ts.atlas.Gen, version: pg.Version, frame: ts.frame, valid: true}
	}
	return p.op
}

// segments splits s into runs the bundled face can shape and runs it can't.
func (ts *typesetter) segments(fi int, s string, fn func(seg string, ok bool)) {
	start, prev := 0, true
	for i, r := range s {
		ok := ts.supported(fi, r)
		if i > start && ok != prev {
			fn(s[start:i], prev)
			start = i
		}
		prev = ok
	}
	if start < len(s) {
		fn(s[start:], prev)
	}
}

func (c *Canvas) typeset() *typesetter {
	if c.ts == nil {
		ts, err := newTypesetter()
		if err != nil {
			panic(err)
		}
		c.ts = ts
	}
	return c.ts
}

// measureShaped is Measure for the atlas and runs modes.
func (c *Canvas) measureShaped(s string, t TextStyle) float32 {
	ts := c.typeset()
	fi := ts.face(t.font())
	if fi < 0 {
		return c.measurePaths(s, t)
	}
	var w float32
	ts.segments(fi, s, func(seg string, ok bool) {
		if ok {
			w += fx(ts.shape(c.shapeKey(fi, seg, t)).advance) / c.Scale
		} else {
			w += c.measurePaths(seg, t)
		}
	})
	return w
}

func (c *Canvas) shapeKey(fi int, s string, t TextStyle) shapeKey {
	return shapeKey{
		face:  fi,
		ppem:  fixed.Int26_6(math.Round(float64(t.Size * c.Scale * 64))),
		text:  s,
		track: fixed.Int26_6(math.Round(float64(t.Spacing * t.Size * c.Scale * 64))),
	}
}

// textShaped is Text for the atlas and runs modes. Runes the bundled face lacks fall back to paths.
func (c *Canvas) textShaped(x, y float32, s string, t TextStyle) float32 {
	ts := c.typeset()
	fi := ts.face(t.font())
	if fi < 0 {
		return c.textPaths(x, y, s, t)
	}
	x0 := x
	base := int(math.Round(float64(y * c.Scale)))
	// Images blend like CSS already, so the colour goes in without SRGBBlend's remap.
	col := t.Color
	ts.segments(fi, s, func(seg string, ok bool) {
		if !ok {
			x += c.textPaths(x, y, seg, t)
			return
		}
		k := c.shapeKey(fi, seg, t)
		run := ts.shape(k)
		if c.Mode == TextRuns {
			c.drawRun(ts, k, run, x, base, col)
		} else {
			c.drawGlyphs(ts, k, run, x, base, col)
		}
		x += fx(run.advance) / c.Scale
	})
	return x - x0
}

func (c *Canvas) drawGlyphs(ts *typesetter, k shapeKey, run *shapedRun, x float32, base int, col color.NRGBA) {
	pen := x * c.Scale
	for _, g := range run.glyphs {
		gx := pen + fx(g.x)
		ix := float32(math.Floor(float64(gx)))
		sub := subStep(gx - ix)
		if sub == 0 && gx-ix > 0.5 {
			ix++
		}
		ck := coverageKey{face: k.face, gid: g.gid, ppem: int32(k.ppem), sub: sub}
		gk := glyphKey{ck, col}
		e, ok := ts.atlas.Lookup(gk)
		if !ok {
			e = ts.atlas.Add(gk, ts.coverage(ck), &ts.gamma)
		}
		if e.Page < 0 {
			continue
		}
		img := ts.pageImageOp(e.Page)
		dst := image.Pt(int(ix)+e.Off.X, base+int(math.Round(float64(fx(g.y))))+e.Off.Y)
		tr := op.Offset(dst.Sub(e.Src.Min)).Push(c.Ops)
		cl := clip.Rect(e.Src).Push(c.Ops)
		img.Add(c.Ops)
		paint.PaintOp{}.Add(c.Ops)
		cl.Pop()
		tr.Pop()
	}
}

func (c *Canvas) drawRun(ts *typesetter, k shapeKey, run *shapedRun, x float32, base int, col color.NRGBA) {
	pen := x * c.Scale
	ix := float32(math.Floor(float64(pen)))
	sub := subStep(pen - ix)
	if sub == 0 && pen-ix > 0.5 {
		ix++
	}
	rk := runKey{k, col, sub}
	ri, ok := ts.runs[rk]
	if !ok {
		ri = c.composeRun(ts, k, run, sub, col)
		ts.runs[rk] = ri
	}
	ri.used = ts.frame
	if ri.op.Size() == (image.Point{}) {
		return
	}
	defer op.Offset(image.Pt(int(ix)+ri.off.X, base+ri.off.Y)).Push(c.Ops).Pop()
	ri.op.Add(c.Ops)
	paint.PaintOp{}.Add(c.Ops)
}

// composeRun draws a whole run's glyphs into one image, positioned from a pen at subpixel step sub.
func (c *Canvas) composeRun(ts *typesetter, k shapeKey, run *shapedRun, sub uint8, col color.NRGBA) *runImage {
	type part struct {
		cov Coverage
		at  image.Point
	}
	var parts []part
	var bounds image.Rectangle
	start := float32(sub) / SubpixelSteps
	for _, g := range run.glyphs {
		gx := start + fx(g.x)
		ix := float32(math.Floor(float64(gx)))
		gs := subStep(gx - ix)
		if gs == 0 && gx-ix > 0.5 {
			ix++
		}
		cov := ts.coverage(coverageKey{face: k.face, gid: g.gid, ppem: int32(k.ppem), sub: gs})
		if cov.Mask == nil {
			continue
		}
		at := image.Pt(int(ix)+cov.Off.X, int(math.Round(float64(fx(g.y))))+cov.Off.Y)
		parts = append(parts, part{cov, at})
		bounds = bounds.Union(image.Rectangle{Min: at, Max: at.Add(cov.Mask.Bounds().Size())})
	}
	if bounds.Empty() {
		return &runImage{}
	}
	img := image.NewRGBA(image.Rectangle{Max: bounds.Size()})
	for _, p := range parts {
		Tint(img, p.at.Sub(bounds.Min), p.cov.Mask, col, &ts.gamma)
	}
	op := paint.NewImageOp(img)
	op.Filter = paint.FilterNearest
	return &runImage{op: op, off: bounds.Min}
}
