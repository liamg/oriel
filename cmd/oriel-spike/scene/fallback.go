package scene

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	gtfont "github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/font/opentype/tables"
)

// The spike's stand-in for CoreText and fontconfig fallback (rendering.md): known system fonts
// first, then a few others. Files are mapped, not read, and only the one face that covers the
// rune is parsed, so a collection never lands on the heap whole.

// fallbackTries caps how many fonts outside the preferred list are opened for one rune.
const fallbackTries = 8

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

// loadFallback finds a face covering r in a system font and parses that face alone. key
// identifies the face (file and index in its collection).
func loadFallback(r rune, tried map[string]bool) (face *gtfont.Face, name, key string) {
	opened := 0
	for _, file := range fallbackCandidates(r) {
		if !isPreferred(file) {
			if opened >= fallbackTries {
				break
			}
			opened++
		}
		if face, i := loadFace(file, r, tried); face != nil {
			name := filepath.Base(file)
			if i > 0 {
				name = fmt.Sprintf("%s#%d", name, i)
			}
			return face, name, fmt.Sprintf("%s#%d", file, i)
		}
	}
	return nil, "", ""
}

// loadFace maps file and parses its first face, not already loaded, whose cmap has r.
func loadFace(file string, r rune, tried map[string]bool) (*gtfont.Face, int) {
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
		f, err := gtfont.NewFont(ld)
		if err != nil {
			continue
		}
		return gtfont.NewFace(f), i
	}
	return nil, 0
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

// fallbackCandidates is replaced in tests.
var fallbackCandidates = systemCandidates

// systemCandidates lists font files to try for r, best first: fontconfig's match on Linux,
// the preferred list on macOS, then fonts in the system directories, symbol, emoji and CJK
// fonts first.
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

var systemFonts []string

func systemFontFiles() []string {
	if systemFonts != nil {
		return systemFonts
	}
	home, _ := os.UserHomeDir()
	files := []string{}
	for _, d := range fontDirs[runtime.GOOS] {
		if strings.HasPrefix(d, "~/") {
			d = filepath.Join(home, d[2:])
		}
		filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() {
				switch strings.ToLower(filepath.Ext(p)) {
				case ".ttf", ".otf", ".ttc", ".otc":
					files = append(files, p)
				}
			}
			return nil
		})
	}
	rank := func(p string) int {
		n := strings.ToLower(filepath.Base(p))
		for i, k := range []string{"symbol", "emoji", "cjk", "dejavu", "unicode"} {
			if strings.Contains(n, k) {
				return i
			}
		}
		return 99
	}
	sort.SliceStable(files, func(i, j int) bool { return rank(files[i]) < rank(files[j]) })
	systemFonts = files
	return files
}
