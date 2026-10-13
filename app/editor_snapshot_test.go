package app

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// roundGo is the fixture 01-ide shows: internal/pricing/round.go, with lines 38 to 51 as the mockup has them.
func roundGo() string {
	var b strings.Builder
	b.WriteString("package pricing\n\nimport (\n\t\"github.com/shopspring/decimal\"\n)\n\n")
	for i := 7; i < 38; i++ {
		fmt.Fprintf(&b, "var tier%d = decimal.NewFromInt(%d)\n", i, i)
	}
	for _, l := range []string{
		"// roundHalfEven rounds v to p decimal places, ties to even.",
		"func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {",
		"    scaled := v.Shift(p)",
		"    floor := scaled.Floor()",
		"    diff := scaled.Sub(floor)",
		"    switch diff.Cmp(half) {",
		"    case 1:",
		"        return floor.Add(one).Shift(-p)",
		"    case 0:",
		"        if floor.IntPart()%2 == 0 { return floor.Shift(-p) }",
		"        return floor.Add(one).Shift(-p)",
		"    }",
		"    return floor.Shift(-p)",
		"}",
	} {
		b.WriteString(l + "\n")
	}
	for i := 52; i <= 80; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	return b.String()
}

// The editor column of 01-ide: from the left edge of the editor to the minimap, from the tabs to the agent dock.
var editorColumn = image.Rect(294, 38, 1150, 773)

// editorScene is the editor on round.go, in a window the size of the mockup's editor column, with the cursor where
// the mockup has it: line 46, on the ':' of "case 0:".
func editorScene(t *testing.T) *errScene {
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "pricing", "round.go")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(roundGo()), 0o644)
	size := render.Point{X: float32(editorColumn.Dx()), Y: float32(editorColumn.Dy())}
	s := newScene(t, "", dir, size)
	s.reg.View(TitleBarView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.ColumnNode() })) // the column starts under it
	s.run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.frame()
	s.frame()
	s.keys("46Gzt38zt46G$")
	s.waitHighlights()
	return s
}

func (s *errScene) editor() *editorExt {
	for _, e := range s.exts {
		if x, ok := e.(*editorExt); ok {
			return x
		}
	}
	return nil
}

// keys types into the editor, a frame after each key, as the window would.
func (s *errScene) keys(k string) {
	for _, key := range keymap.Parse(k) {
		s.editor().e.Key(key)
		s.frame()
	}
}

func (s *errScene) waitHighlights() {
	for _, h := range s.editor().docs {
		if h.doc != nil {
			h.doc.Wait()
		}
	}
	s.frame()
}

// ED-12: snapshots of the editor pane in normal, insert and Visual-block mode, with a closed fold, and split.
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
			s.keys(tt.keys)
			s.waitHighlights()
			s.golden(tt.name)
			text := s.frameText()
			for _, w := range tt.has {
				if !strings.Contains(text, w) {
					t.Errorf("the frame lacks %q:\n%s", w, text)
				}
			}
		})
	}
}

// "a golden PNG of the fixture holds against the mockup's editor column": the tabs, breadcrumb and lines 38 to 46,
// above the thread card the mockup shows there. What's left of the difference is other tickets' work: the added
// lines' band and gutter (FG-2), the inline "← agent" text (ED-7), the thread marker (AI-5), the tabs' file icons
// and the breadcrumb's symbol and chips. The bound is just over today's 0.058 and 8.7%, so it only tightens.
func TestEditorAgainstTheMockup(t *testing.T) {
	s := editorScene(t)
	s.frame()
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../_design/mockups/01-ide.png")
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

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
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
func typingScene(t testing.TB) *errScene {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big.go"), []byte(bigGo()), 0o644)
	s := newScene(t, "", dir, render.Point{X: 1600, Y: 1000})
	s.run("editor.open", registry.Args{"path": "big.go"})
	s.frame()
	s.keys("5000Go")
	s.waitHighlights()
	return s
}

// keyToFrame types one key and builds and records the frame that shows it.
func (s *errScene) keyToFrame(k string) {
	s.editor().e.Key(k)
	s.frame()
}

func BenchmarkEditorTyping(b *testing.B) {
	s := typingScene(b)
	b.ResetTimer()
	for i := range b.N {
		s.keyToFrame(string(rune('a' + i%26)))
	}
}

// PLAN.md's budget: keypress to frame under 8 ms while typing in a 10,000-line Go file.
func TestEditorTypingBudget(t *testing.T) {
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing needs a build without the race detector or coverage")
	}
	s := typingScene(t)
	const keys = 200
	times := make([]time.Duration, keys)
	var total time.Duration
	for i := range keys {
		start := time.Now()
		s.keyToFrame(string(rune('a' + i%26)))
		times[i] = time.Since(start)
		total += times[i]
	}
	slices.Sort(times)
	mean := total / keys
	t.Logf("keypress to frame: mean %v, p95 %v, worst %v over %d keys", mean, times[keys*95/100], times[keys-1], keys)
	// ED-14: "TestEditorTypingBudget's worst keypress to frame is under 8 ms, not only its mean."
	if worst := times[keys-1]; worst > 8*time.Millisecond {
		t.Errorf("the worst keypress to frame took %v; the budget is 8 ms", worst)
	}
}
