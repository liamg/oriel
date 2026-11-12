package typeset

import (
	"bytes"
	"embed"
	"fmt"
	"image/color"
	"io/fs"
	"path"
	"sync"
	"sync/atomic"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
)

//go:embed fonts/*/*.ttf
var fontFS embed.FS

var faceIDs atomic.Int32

// Face is one loaded font face.
type Face struct {
	ID     int32 // unique in the process, for cache keys
	Name   string
	Family string
	Weight Weight
	Italic bool
	face   *gtfont.Face
}

func newFace(f *gtfont.Font, name string, d gtfont.Description) *Face {
	return &Face{
		ID:     faceIDs.Add(1),
		Name:   name,
		Family: d.Family,
		Weight: Weight(d.Aspect.Weight),
		Italic: d.Aspect.Style == gtfont.StyleItalic,
		face:   gtfont.NewFace(f),
	}
}

// Has reports whether the face has a glyph for r.
func (f *Face) Has(r rune) bool {
	_, ok := f.face.NominalGlyph(r)
	return ok
}

// layer is one layer of a colour glyph: an outline in a palette colour, or in the text's own (Foreground).
type layer struct {
	ID         GlyphID
	Color      color.NRGBA
	Foreground bool
}

// layers are gid's COLR version 0 layers, bottom first, in the default palette's colours; palette index 0xFFFF is the
// text's own colour. None when the face draws gid as one outline.
func (f *Face) layers(gid GlyphID) []layer {
	p, ok := f.face.GlyphDataColor(gtfont.GID(gid))
	if !ok {
		return nil
	}
	ls, ok := p.Paint.(tables.PaintColrLayersResolved)
	if !ok || len(f.face.CPAL) == 0 {
		return nil
	}
	pal := f.face.CPAL[0]
	out := make([]layer, 0, len(ls))
	for _, l := range ls {
		if l.PaletteIndex == 0xFFFF {
			out = append(out, layer{ID: GlyphID(l.GlyphID), Foreground: true})
			continue
		}
		if int(l.PaletteIndex) >= len(pal) {
			continue
		}
		c := pal[l.PaletteIndex]
		out = append(out, layer{ID: GlyphID(l.GlyphID), Color: color.NRGBA{R: c.Red, G: c.Green, B: c.Blue, A: c.Alpha}})
	}
	return out
}

// Upem is the face's units per em.
func (f *Face) Upem() uint16 { return f.face.Upem() }

func (f *Face) outline(gid GlyphID) ([]ot.Segment, bool) {
	o, ok := f.face.GlyphDataOutline(gtfont.GID(gid))
	return o.Segments, ok
}

var (
	bundleOnce sync.Once
	bundle     []*Face
	bundleErr  error
)

// Bundled returns the faces shipped with Oriel, parsed once.
func Bundled() ([]*Face, error) {
	bundleOnce.Do(func() {
		files, _ := fs.Glob(fontFS, "fonts/*/*.ttf")
		for _, file := range files {
			b, err := fontFS.ReadFile(file)
			if err != nil {
				bundleErr = err
				return
			}
			ld, err := ot.NewLoader(bytes.NewReader(b))
			if err != nil {
				bundleErr = fmt.Errorf("%s: %w", path.Base(file), err)
				return
			}
			f, err := gtfont.NewFont(ld)
			if err != nil {
				bundleErr = fmt.Errorf("%s: %w", path.Base(file), err)
				return
			}
			d, _ := gtfont.Describe(ld, nil)
			bundle = append(bundle, newFace(f, path.Base(file), d))
		}
	})
	return bundle, bundleErr
}

// match picks the face for f among faces: the same family, the same style if there is one,
// then the nearest weight. Unknown families get the UI face.
func match(faces []*Face, f FontSpec) *Face {
	w := f.Weight
	if w == 0 {
		w = Normal
	}
	var best *Face
	bestScore := 0
	for _, fc := range faces {
		if fc.Family != f.Family {
			continue
		}
		score := abs(int(fc.Weight) - int(w))
		if fc.Italic != f.Italic {
			score += 10_000
		}
		if best == nil || score < bestScore {
			best, bestScore = fc, score
		}
	}
	if best == nil && f.Family != UI {
		g := f
		g.Family = UI
		return match(faces, g)
	}
	return best
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
