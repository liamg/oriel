package editor_test

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
)

// Snapshots of the editor pane in normal, insert and Visual-block mode, with a closed fold, and split.
func TestEditorSnapshots(t *testing.T) {
	for _, tt := range []struct {
		name, keys string
		has        []string
	}{
		{"editor-normal", "", []string{"roundHalfEven", "NORMAL", "46:11", "round.go"}},
		{"editor-insert", "a // ok<Esc>a", []string{"INSERT", "// ok"}},
		{"editor-visual-block", "41G05l<C-v>3jl", []string{"V-BLOCK"}},
		{"editor-fold", "42Gzf6j", []string{"7 lines"}},
		{"editor-split", ":split<CR>", []string{"NORMAL"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := editorScene(t)
			keys(s, tt.keys)
			waitHighlights(s)
			s.Golden(tt.name)
			text := s.Text()
			for _, w := range tt.has {
				if !strings.Contains(text, w) {
					t.Errorf("the frame lacks %q:\n%s", w, text)
				}
			}
		})
	}
}

// A golden PNG of the fixture holds against the mockup's editor column: the tabs, breadcrumb and lines 38 to 46,
// above the thread card the mockup shows there. What's left of the difference is drawn by other features: the added
// lines' band and gutter, the inline "← agent" text, the thread marker, the tabs' file icons and the breadcrumb's
// symbol and chips. The bound is just over the current 0.058 and 8.7%, so it only tightens.
func TestEditorAgainstTheMockup(t *testing.T) {
	s := editorScene(t)
	s.Frame()
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../../testdata/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	const rows = 254 // tabs, breadcrumb and lines 38 to 46
	want := image.NewRGBA(image.Rect(0, 0, editorColumn.Dx(), rows))
	draw.Draw(want, want.Bounds(), mock, editorColumn.Min, draw.Src)
	got := image.NewRGBA(want.Bounds())
	draw.Draw(got, got.Bounds(), img, image.Point{}, draw.Src)
	d, side := snapshot.Compare(want, got, 0.1)
	t.Logf("editor column against 01-ide: %s", d)
	if d.Mean > 0.065 || d.Off > 0.095 {
		os.WriteFile("testdata/editor-mockup.failed.png", encodePNG(side), 0o644)
		t.Errorf("the editor column is %s off the mockup (side by side in testdata/editor-mockup.failed.png)", d)
	}
}

// bigGo is a 10,000-line Go file.
func bigGo() string {
	var b strings.Builder
	b.WriteString("package big\n\nimport \"fmt\"\n\n")
	for i := 0; b.Len() < 1<<20 && strings.Count(b.String(), "\n") < 9990; i++ {
		fmt.Fprintf(&b, "// f%d prints its number.\nfunc f%d(n int) string {\n\treturn fmt.Sprintf(\"%%d:%%d\", n, %d)\n}\n\n", i, i, i)
	}
	return b.String()
}

// typingScene is the editor on a 10,000-line Go file, half way down, in insert mode.
func typingScene(t testing.TB) *apptest.Scene {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(bigGo()), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1600, Y: 1000}})
	s.Run("editor.open", registry.Args{"path": "big.go"})
	s.Frame()
	keys(s, "5000Go")
	waitHighlights(s)
	return s
}

// keyToFrame types one key and builds and records the frame that shows it.
func keyToFrame(s *apptest.Scene, k string) {
	engine(s).Key(k)
	s.Frame()
}

func BenchmarkEditorTyping(b *testing.B) {
	s := typingScene(b)
	b.ResetTimer()
	for i := range b.N {
		keyToFrame(s, string(rune('a'+i%26)))
	}
}
