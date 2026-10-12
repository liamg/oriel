package scene

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"sync"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

//go:embed fonts/*/*.ttf
var fontFS embed.FS

// Typefaces, as named by the bundled fonts.
const (
	Mono    font.Typeface = "JetBrains Mono"
	UI      font.Typeface = "Inter"
	Display font.Typeface = "Space Grotesk"
)

var (
	collOnce sync.Once
	coll     []font.FontFace
	collErr  error
)

// Collection parses the bundled fonts once.
func Collection() ([]font.FontFace, error) {
	collOnce.Do(func() {
		files, _ := fs.Glob(fontFS, "fonts/*/*.ttf")
		for _, f := range files {
			b, err := fontFS.ReadFile(f)
			if err != nil {
				collErr = err
				return
			}
			face, err := opentype.Parse(b)
			if err != nil {
				collErr = fmt.Errorf("%s: %w", path.Base(f), err)
				return
			}
			coll = append(coll, font.FontFace{Font: face.Font(), Face: face})
		}
	})
	return coll, collErr
}

// Fonts is the bundled faces plus system faces loaded one at a time, the first time a rune
// needs one. Nothing scans or indexes the system fonts.
type Fonts struct {
	// NoFallback leaves runes the bundled faces lack as tofu.
	NoFallback bool

	coll    []font.FontFace
	known   map[rune]bool
	missing []rune
	loaded  map[string]bool
	names   []string
	shaper  *text.Shaper
}

// NewFonts parses the bundled fonts.
func NewFonts() (*Fonts, error) {
	c, err := Collection()
	if err != nil {
		return nil, err
	}
	f := &Fonts{coll: append([]font.FontFace(nil), c...), known: map[rune]bool{}, loaded: map[string]bool{}}
	f.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(f.coll))
	return f, nil
}

// Shaper shapes with every face loaded so far.
func (f *Fonts) Shaper() *text.Shaper { return f.shaper }

// Fallbacks lists the system fonts loaded so far, and the runes no fallback covered.
func (f *Fonts) Fallbacks() (files []string, missing []rune) { return f.names, f.missing }

// Need loads system fallback faces for any rune in s the loaded faces lack.
func (f *Fonts) Need(s string) {
	var want []rune
	for _, r := range s {
		if r < 0x80 || f.known[r] {
			continue
		}
		f.known[r] = true
		if !f.covers(f.coll, r) {
			want = append(want, r)
		}
	}
	if len(want) == 0 || f.NoFallback {
		f.missing = append(f.missing, want...)
		return
	}
	added := false
	for _, r := range want {
		if f.covers(f.coll, r) {
			continue
		}
		if faces, name := loadFallback(r, f.loaded); faces != nil {
			f.coll = append(f.coll, faces...)
			f.names = append(f.names, name)
			added = true
		} else {
			f.missing = append(f.missing, r)
		}
	}
	if added {
		f.shaper = text.NewShaper(text.NoSystemFonts(), text.WithCollection(f.coll))
	}
}

func (f *Fonts) covers(coll []font.FontFace, r rune) bool {
	for _, c := range coll {
		if of, ok := c.Face.(opentype.Face); ok {
			if _, ok := of.Face().NominalGlyph(r); ok {
				return true
			}
		}
	}
	return false
}

// FaceNames lists the bundled faces, for the report.
func FaceNames() []string {
	c, _ := Collection()
	var out []string
	for _, f := range c {
		out = append(out, strings.TrimSpace(fmt.Sprintf("%s %v %v", f.Font.Typeface, f.Font.Weight, f.Font.Style)))
	}
	return out
}
