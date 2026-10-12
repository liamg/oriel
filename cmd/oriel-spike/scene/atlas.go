package scene

import (
	"image"
	"image/color"
	"math"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"golang.org/x/image/vector"
)

// SubpixelSteps is how many horizontal subpixel positions each glyph is rasterised at.
const SubpixelSteps = 4

// coverageKey identifies one rasterised glyph, independent of colour.
type coverageKey struct {
	face int
	gid  font.GID
	ppem int32 // 26.6 pixels per em
	sub  uint8 // subpixel x step, 0..SubpixelSteps-1
}

// Coverage is a glyph's antialiased mask, placed relative to the pen: the pen sits at
// (-Off.X, -Off.Y) in Mask, with the baseline at y = -Off.Y.
type Coverage struct {
	Mask *image.Alpha // nil for empty glyphs such as space
	Off  image.Point
}

// rasterise draws an outline (font units, y up) at scale px per unit, shifted right by sub px.
func rasterise(segs []ot.Segment, scale, sub float32) Coverage {
	if len(segs) == 0 {
		return Coverage{}
	}
	minX, minY := float32(math.Inf(1)), float32(math.Inf(1))
	maxX, maxY := float32(math.Inf(-1)), float32(math.Inf(-1))
	for _, s := range segs {
		for _, p := range s.ArgsSlice() {
			x, y := p.X*scale+sub, -p.Y*scale
			minX, maxX = min(minX, x), max(maxX, x)
			minY, maxY = min(minY, y), max(maxY, y)
		}
	}
	x0, y0 := int(math.Floor(float64(minX))), int(math.Floor(float64(minY)))
	x1, y1 := int(math.Ceil(float64(maxX))), int(math.Ceil(float64(maxY)))
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return Coverage{}
	}
	var r vector.Rasterizer
	r.Reset(w, h)
	pt := func(p ot.SegmentPoint) (float32, float32) {
		return p.X*scale + sub - float32(x0), -p.Y*scale - float32(y0)
	}
	for _, s := range segs {
		a := s.Args
		switch s.Op {
		case ot.SegmentOpMoveTo:
			r.MoveTo(pt(a[0]))
		case ot.SegmentOpLineTo:
			r.LineTo(pt(a[0]))
		case ot.SegmentOpQuadTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			r.QuadTo(bx, by, cx, cy)
		case ot.SegmentOpCubeTo:
			bx, by := pt(a[0])
			cx, cy := pt(a[1])
			dx, dy := pt(a[2])
			r.CubeTo(bx, by, cx, cy, dx, dy)
		}
	}
	r.ClosePath()
	mask := image.NewAlpha(image.Rect(0, 0, w, h))
	r.Draw(mask, mask.Bounds(), image.Opaque, image.Point{})
	return Coverage{Mask: mask, Off: image.Pt(x0, y0)}
}

// TextGamma maps glyph coverage to the alpha written into the atlas so that Gio, which samples
// premultiplied sRGB textures and blends in linear light, shows what CSS's sRGB blending would:
// light text is solved over black, dark text over white. Dark text also gets stem darkening.
type TextGamma struct {
	Darken float64 // extra weight for dark text: coverage^(1/(1+Darken))
	tables map[color.NRGBA]*[256]uint8
}

// Alpha maps coverage c (0..255) for text colour col.
func (g *TextGamma) Alpha(c uint8, col color.NRGBA) uint8 {
	col.A = 255
	t, ok := g.tables[col]
	if !ok {
		t = g.build(col)
		if g.tables == nil {
			g.tables = map[color.NRGBA]*[256]uint8{}
		}
		g.tables[col] = t
	}
	return t[c]
}

func (g *TextGamma) build(col color.NRGBA) *[256]uint8 {
	var t [256]uint8
	light := luma(col) >= 0.5
	// The channel with the most contrast against the assumed backdrop.
	c, bg := float64(max(col.R, col.G, col.B))/255, 0.0
	if !light {
		c, bg = float64(min(col.R, col.G, col.B))/255, 1
	}
	for i := range 256 {
		a := float64(i) / 255
		if !light {
			a = math.Pow(a, 1/(1+g.Darken))
		}
		switch i {
		case 0, 255:
			t[i] = uint8(i)
		default:
			t[i] = uint8(math.Round(255 * solveAlpha(c, bg, c*a+(1-a)*bg)))
		}
	}
	return &t
}

// solveAlpha finds a' with enc(lin(c·a') + (1-a')·lin(bg)) = want, by bisection.
func solveAlpha(c, bg, want float64) float64 {
	f := func(a float64) float64 { return enc(lin(c*a)+(1-a)*lin(bg)) - want }
	lo, hi := 0.0, 1.0
	rising := f(hi) >= f(lo)
	for range 40 {
		mid := (lo + hi) / 2
		if (f(mid) < 0) == rising {
			lo = mid
		} else {
			hi = mid
		}
	}
	return (lo + hi) / 2
}

func enc(v float64) float64 {
	if v <= 0.0031308 {
		return 12.92 * v
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

func luma(c color.NRGBA) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

// Tint writes cov in colour col into dst at p, as premultiplied sRGB.
func Tint(dst *image.RGBA, p image.Point, cov *image.Alpha, col color.NRGBA, g *TextGamma) {
	b := cov.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := cov.AlphaAt(x, y).A
			if c == 0 {
				continue
			}
			a := uint32(g.Alpha(c, col)) * uint32(col.A) / 255
			i := dst.PixOffset(p.X+x-b.Min.X, p.Y+y-b.Min.Y)
			px := dst.Pix[i : i+4 : i+4]
			// Source over, premultiplied.
			inv := 255 - a
			px[0] = uint8((uint32(col.R)*a + uint32(px[0])*inv) / 255)
			px[1] = uint8((uint32(col.G)*a + uint32(px[1])*inv) / 255)
			px[2] = uint8((uint32(col.B)*a + uint32(px[2])*inv) / 255)
			px[3] = uint8(a + uint32(px[3])*inv/255)
		}
	}
}

// glyphKey identifies one glyph in one colour in the atlas.
type glyphKey struct {
	coverageKey
	col color.NRGBA
}

// AtlasEntry is where a glyph sits in the atlas. A zero Src means the glyph draws nothing.
type AtlasEntry struct {
	Page int
	Src  image.Rectangle
	Off  image.Point // from the pen to Src.Min, in device pixels
}

type shelf struct{ y, h, x int }

// AtlasPage is one texture of the atlas. Version changes whenever a glyph is added.
type AtlasPage struct {
	Img     *image.RGBA
	Version int
	shelves []shelf
	next    int // y of the next shelf
}

// Atlas packs tinted glyphs into fixed-size pages with a shelf packer. When every page is
// full it starts over with fresh pages (Gen increments), so pages already handed to the
// GPU are never overwritten.
type Atlas struct {
	PageSize int
	MaxPages int
	Pages    []*AtlasPage
	Gen      int
	entries  map[glyphKey]AtlasEntry
}

// NewAtlas returns an empty atlas.
func NewAtlas(pageSize, maxPages int) *Atlas {
	return &Atlas{PageSize: pageSize, MaxPages: maxPages, entries: map[glyphKey]AtlasEntry{}}
}

// Len is the number of glyphs held.
func (a *Atlas) Len() int { return len(a.entries) }

// Lookup returns a glyph already in the atlas.
func (a *Atlas) Lookup(k glyphKey) (AtlasEntry, bool) {
	e, ok := a.entries[k]
	return e, ok
}

// Add places cov tinted with k.col, evicting everything if the atlas is full.
func (a *Atlas) Add(k glyphKey, cov Coverage, g *TextGamma) AtlasEntry {
	if cov.Mask == nil {
		e := AtlasEntry{Page: -1}
		a.entries[k] = e
		return e
	}
	size := cov.Mask.Bounds().Size()
	page, r, ok := a.place(size)
	if !ok {
		a.Reset()
		page, r, ok = a.place(size)
		if !ok {
			// Bigger than a page: drop it rather than loop.
			e := AtlasEntry{Page: -1}
			a.entries[k] = e
			return e
		}
	}
	p := a.Pages[page]
	Tint(p.Img, r.Min, cov.Mask, k.col, g)
	p.Version++
	e := AtlasEntry{Page: page, Src: r, Off: cov.Off}
	a.entries[k] = e
	return e
}

// Reset drops every glyph and starts new pages.
func (a *Atlas) Reset() {
	a.Pages = nil
	a.entries = map[glyphKey]AtlasEntry{}
	a.Gen++
}

const atlasPad = 1

func (a *Atlas) place(size image.Point) (int, image.Rectangle, bool) {
	w, h := size.X+atlasPad, size.Y+atlasPad
	if w > a.PageSize || h > a.PageSize {
		return 0, image.Rectangle{}, false
	}
	for pi, p := range a.Pages {
		for i := range p.shelves {
			s := &p.shelves[i]
			if h <= s.h && h*4 >= s.h*3 && s.x+w <= a.PageSize {
				r := image.Rect(s.x, s.y, s.x+size.X, s.y+size.Y)
				s.x += w
				return pi, r, true
			}
		}
		if p.next+h <= a.PageSize {
			p.shelves = append(p.shelves, shelf{y: p.next, h: h, x: w})
			r := image.Rect(0, p.next, size.X, p.next+size.Y)
			p.next += h
			return pi, r, true
		}
	}
	if len(a.Pages) >= a.MaxPages {
		return 0, image.Rectangle{}, false
	}
	a.Pages = append(a.Pages, &AtlasPage{Img: image.NewRGBA(image.Rect(0, 0, a.PageSize, a.PageSize))})
	return a.place(size)
}
