package typeset

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
)

// Fallback faces come from system fonts, one face at a time, the first time a rune needs one.
// Files are mapped, not read, only the face that covers the rune is parsed, and loading runs on
// a worker, never on the frame: the rune shows as tofu until its face lands.

const (
	fallbackTries = 8   // fonts outside the preferred list opened for one rune
	maxFallbacks  = 24  // faces held at once
	maxQueue      = 512 // runes waiting for a face
)

// Apple Color Emoji is left out: its colour bitmaps are one huge table that go-text copies to
// the heap whole, and the atlas only rasterises outlines.
var preferredFallbacks = map[string][]string{
	"darwin": {
		"/System/Library/Fonts/Apple Symbols.ttf",
		"/System/Library/Fonts/Menlo.ttc",
		"/System/Library/Fonts/Apple Braille.ttf",
		"/System/Library/Fonts/Hiragino Sans GB.ttc",
		"/System/Library/Fonts/AppleSDGothicNeo.ttc",
		"/System/Library/Fonts/STHeiti Medium.ttc",
		"/System/Library/Fonts/Supplemental/Arial Unicode.ttf",
	},
}

var fontDirs = map[string][]string{
	"darwin": {"/System/Library/Fonts", "/Library/Fonts", "~/Library/Fonts"},
	"linux":  {"/usr/share/fonts", "/usr/local/share/fonts", "~/.local/share/fonts", "~/.fonts"},
}

// fallbacks loads faces for runes the bundled faces lack.
type fallbacks struct {
	onLoad func()
	find   func(r rune) []string // candidate files, best first

	mu        sync.Mutex
	requested map[rune]bool
	work      chan rune
	done      chan struct{} // closed when the worker has stopped
	closed    bool
	landed    []*Face
	names     []string
	missing   []rune
}

func newFallbacks(onLoad func()) *fallbacks {
	return &fallbacks{onLoad: onLoad, find: systemCandidates, requested: map[rune]bool{}}
}

// request asks the worker for a face covering r, once per rune.
func (f *fallbacks) request(r rune) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed || f.requested[r] || len(f.names) >= maxFallbacks {
		return
	}
	f.requested[r] = true
	if f.work == nil {
		f.work = make(chan rune, maxQueue)
		f.done = make(chan struct{})
		go f.worker()
	}
	select {
	case f.work <- r:
	default: // the queue is full; the rune stays tofu
	}
}

// close stops the worker; runes asked for after it stay tofu.
func (f *fallbacks) close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed && f.work != nil {
		close(f.work)
	}
	f.closed = true
}

// take returns the faces that have landed since the last call.
func (f *fallbacks) take() []*Face {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.landed
	f.landed = nil
	return l
}

func (f *fallbacks) list() ([]string, []rune) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.names...), append([]rune(nil), f.missing...)
}

func (f *fallbacks) worker() {
	defer close(f.done)
	var faces []*Face
	tried := map[string]bool{}
	for r := range f.work {
		covered := false
		for _, fc := range faces {
			covered = covered || fc.Has(r)
		}
		if covered {
			continue
		}
		face, key := f.load(r, tried)
		f.mu.Lock()
		if face == nil {
			f.missing = append(f.missing, r)
			f.mu.Unlock()
			continue
		}
		tried[key] = true
		faces = append(faces, face)
		f.names = append(f.names, face.Name)
		f.landed = append(f.landed, face)
		f.mu.Unlock()
		if f.onLoad != nil {
			f.onLoad()
		}
	}
}

// load finds a face covering r in a system font and parses that face alone. key names the face
// (file and index in its collection).
func (f *fallbacks) load(r rune, tried map[string]bool) (*Face, string) {
	opened := 0
	for _, file := range f.find(r) {
		if !isPreferred(file) {
			if opened >= fallbackTries {
				break
			}
			opened++
		}
		if face, i := loadFace(file, r, tried); face != nil {
			return face, fmt.Sprintf("%s#%d", file, i)
		}
	}
	return nil, ""
}

// loadFace maps file and parses its first face, not already loaded, whose cmap has r.
func loadFace(file string, r rune, tried map[string]bool) (*Face, int) {
	data, unmap, err := mapFile(file)
	if err != nil {
		return nil, 0
	}
	defer unmap()
	lds, err := ot.NewLoaders(bytes.NewReader(data))
	if err != nil {
		return nil, 0
	}
	for i, ld := range lds {
		if tried[fmt.Sprintf("%s#%d", file, i)] || !cmapHas(ld, r) {
			continue
		}
		// go-text copies the tables it reads, so nothing refers to data after this.
		fnt, err := gtfont.NewFont(ld)
		if err != nil {
			continue
		}
		d, _ := gtfont.Describe(ld, nil)
		name := filepath.Base(file)
		if i > 0 {
			name = fmt.Sprintf("%s#%d", name, i)
		}
		return newFace(fnt, name, d), i
	}
	return nil, 0
}

// fontFilesIn are the font files under dirs; "~/" is the home folder.
func fontFilesIn(dirs []string) []string {
	home, _ := os.UserHomeDir()
	var out []string
	for _, d := range dirs {
		if strings.HasPrefix(d, "~/") {
			d = filepath.Join(home, d[2:])
		}
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				switch strings.ToLower(filepath.Ext(p)) {
				case ".ttf", ".otf", ".ttc", ".otc":
					out = append(out, p)
				}
			}
			return nil
		})
	}
	return out
}

// cmapHas reads only the cmap table.
func cmapHas(ld *ot.Loader, r rune) bool {
	raw, err := ld.RawTable(ot.MustNewTag("cmap"))
	if err != nil {
		return false
	}
	tb, _, err := tables.ParseCmap(raw)
	if err != nil {
		return false
	}
	cm, _, err := gtfont.ProcessCmap(tb, tables.FPNone)
	if err != nil {
		return false
	}
	_, ok := cm.Lookup(r)
	return ok
}

func isPreferred(file string) bool {
	for _, p := range preferredFallbacks[runtime.GOOS] {
		if p == file {
			return true
		}
	}
	return false
}

// systemCandidates lists font files to try for r, best first: fontconfig's match on Linux, the
// preferred list on macOS, then fonts in the system directories, symbol, emoji and CJK fonts first.
func systemCandidates(r rune) []string {
	var out []string
	if runtime.GOOS == "linux" {
		if b, err := exec.Command("fc-match", "-f", "%{file}", fmt.Sprintf(":charset=%x", r)).Output(); err == nil && len(b) > 0 {
			out = append(out, string(b))
		}
	}
	for _, p := range preferredFallbacks[runtime.GOOS] {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return append(out, systemFontFiles()...)
}

var (
	systemFontsOnce sync.Once
	systemFonts     []string
)

func systemFontFiles() []string {
	systemFontsOnce.Do(func() {
		systemFonts = fontFilesIn(fontDirs[runtime.GOOS])
		rank := func(p string) int {
			n := strings.ToLower(filepath.Base(p))
			for i, k := range []string{"symbol", "emoji", "cjk", "dejavu", "unicode"} {
				if strings.Contains(n, k) {
					return i
				}
			}
			return 99
		}
		slices.SortStableFunc(systemFonts, func(a, b string) int { return cmp.Compare(rank(a), rank(b)) })
	})
	return systemFonts
}
