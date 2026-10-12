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

// NewShaper returns a shaper over the bundled fonts, with system fonts as fallback unless noSystem.
func NewShaper(noSystem bool) (*text.Shaper, error) {
	c, err := Collection()
	if err != nil {
		return nil, err
	}
	opts := []text.ShaperOption{text.WithCollection(c)}
	if noSystem {
		opts = append(opts, text.NoSystemFonts())
	}
	return text.NewShaper(opts...), nil
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
