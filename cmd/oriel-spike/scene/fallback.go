package scene

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"gioui.org/font"
	"gioui.org/font/opentype"
)

// The spike's stand-in for CoreText and fontconfig fallback (rendering.md): known system fonts
// first, then a few others, parsed one at a time until one covers the rune.

// fallbackTries caps how many fonts outside the preferred list are parsed for one rune.
const fallbackTries = 8

var preferredFallbacks = map[string][]string{
	"darwin": {
		"/System/Library/Fonts/Apple Symbols.ttf",
		"/System/Library/Fonts/Menlo.ttc",
		"/System/Library/Fonts/Apple Braille.ttf",
		"/System/Library/Fonts/Apple Color Emoji.ttc",
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

// loadFallback finds a system font covering r that isn't loaded yet and parses it.
func loadFallback(r rune, loaded map[string]bool) ([]font.FontFace, string) {
	tries := 0
	for _, file := range fallbackCandidates(r) {
		if loaded[file] {
			continue
		}
		if !isPreferred(file) {
			if tries >= fallbackTries {
				break
			}
			tries++
		}
		faces, err := parseFontFile(file)
		if err != nil || !coversAny(faces, r) {
			continue
		}
		loaded[file] = true
		return faces, filepath.Base(file)
	}
	return nil, ""
}

func isPreferred(file string) bool {
	for _, p := range preferredFallbacks[runtime.GOOS] {
		if p == file {
			return true
		}
	}
	return false
}

func parseFontFile(file string) ([]font.FontFace, error) {
	b, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	return opentype.ParseCollection(b)
}

func coversAny(faces []font.FontFace, r rune) bool {
	for _, f := range faces {
		if of, ok := f.Face.(opentype.Face); ok {
			if _, ok := of.Face().NominalGlyph(r); ok {
				return true
			}
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
