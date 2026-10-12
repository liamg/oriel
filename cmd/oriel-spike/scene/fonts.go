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
	gtfont "github.com/go-text/typesetting/font"
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

// Fonts is the bundled faces, shaped through Gio, plus system faces for runes they lack. A
// fallback face loads on a worker the first time a rune needs one, and is used by the atlas text
// path once it lands; until then the rune draws as tofu. Nothing scans or indexes the system fonts.
type Fonts struct {
	// NoFallback leaves runes the bundled faces lack as tofu.
	NoFallback bool
	// OnLoad is called, on the worker, when a fallback face has loaded.
	OnLoad func()

	shaper    *text.Shaper
	requested map[rune]bool
	work      chan rune
	mu        sync.Mutex
	landed    []Fallback
	names     []string
	missing   []rune
}

// Fallback is a system face loaded for runes the bundled faces lack.
type Fallback struct {
	Face *gtfont.Face
	Name string
}

// NewFonts parses the bundled fonts.
func NewFonts() (*Fonts, error) {
	c, err := Collection()
	if err != nil {
		return nil, err
	}
	return &Fonts{
		shaper:    text.NewShaper(text.NoSystemFonts(), text.WithCollection(c)),
		requested: map[rune]bool{},
	}, nil
}

// Shaper shapes with the bundled faces.
func (f *Fonts) Shaper() *text.Shaper { return f.shaper }

// Fallbacks lists the system fonts loaded so far, and the runes no fallback covered.
func (f *Fonts) Fallbacks() (files []string, missing []rune) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.names...), append([]rune(nil), f.missing...)
}

// Request asks the worker for a face covering r, once per rune.
func (f *Fonts) Request(r rune) {
	if f.NoFallback || f.requested[r] {
		return
	}
	f.requested[r] = true
	if f.work == nil {
		f.work = make(chan rune, 1024)
		go f.worker()
	}
	select {
	case f.work <- r:
	default: // the queue is full; the rune stays tofu
	}
}

// Landed returns the faces loaded since the last call.
func (f *Fonts) Landed() []Fallback {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.landed
	f.landed = nil
	return l
}

func (f *Fonts) worker() {
	var faces []*gtfont.Face
	tried := map[string]bool{}
	for r := range f.work {
		covered := false
		for _, face := range faces {
			if _, ok := face.NominalGlyph(r); ok {
				covered = true
				break
			}
		}
		if covered {
			continue
		}
		face, name, key := loadFallback(r, tried)
		f.mu.Lock()
		if face == nil {
			f.missing = append(f.missing, r)
			f.mu.Unlock()
			continue
		}
		tried[key] = true
		faces = append(faces, face)
		f.names = append(f.names, name)
		f.landed = append(f.landed, Fallback{face, name})
		f.mu.Unlock()
		if f.OnLoad != nil {
			f.OnLoad()
		}
	}
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
