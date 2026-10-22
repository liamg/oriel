package editor_test

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/tools/perf"
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

// The typing budget: keypress to frame under 8 ms while typing in a 10,000-line Go file.
func TestEditorTypingBudget(t *testing.T) {
	perf.Timed(t)
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing needs a build without the race detector or coverage")
	}
	s := typingScene(t)
	const keys = 200
	// Each key's time is its best over three runs of the same keys (perf.Times): one the scheduler preempted isn't
	// slow, one that's slow every time is.
	times := perf.Times(3, keys, nil, func(i int) { keyToFrame(s, string(rune('a'+i%26))) })
	waitHighlights(s) // the last reparse done before the scene closes
	var total time.Duration
	for _, d := range times {
		total += d
	}
	slices.Sort(times)
	mean := total / keys
	t.Logf("keypress to frame: mean %v, p95 %v, worst %v over %d keys", mean, times[keys*95/100], times[keys-1], keys)
	// The worst keypress to frame is under 8 ms, not only the mean.
	if worst := times[keys-1]; worst > 8*time.Millisecond {
		t.Errorf("the worst keypress to frame took %v; the budget is 8 ms", worst)
	}
}

// The per-frame target on BenchmarkEditorTyping: a typing frame allocates under 0.6MB. Bytes, unlike time,
// don't move with the machine's load.
func TestEditorTypingFrameAllocs(t *testing.T) {
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("allocation counts need a build without the race detector or coverage")
	}
	s := typingScene(t)
	for i := range 50 {
		keyToFrame(s, string(rune('a'+i%26)))
	}
	const keys = 200
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := range keys {
		keyToFrame(s, string(rune('a'+i%26)))
	}
	runtime.ReadMemStats(&after)
	waitHighlights(s) // the last reparse done before the scene closes
	bytes, allocs := (after.TotalAlloc-before.TotalAlloc)/keys, (after.Mallocs-before.Mallocs)/keys
	t.Logf("a typing frame: %d B, %d allocations", bytes, allocs)
	if bytes > 600<<10 {
		t.Errorf("a typing frame allocates %d B; the budget is 0.6MB", bytes)
	}
}

// The per-frame target on BenchmarkEditorTyping: a typing frame takes under 2ms on average, timed on the
// loop's thread against reference work (perf.Times) so a loaded machine doesn't fail it.
func TestEditorTypingFrameBudget(t *testing.T) {
	perf.Timed(t)
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing needs a build without the race detector or coverage")
	}
	s := typingScene(t)
	const keys = 200
	times := perf.Times(3, keys, nil, func(i int) { keyToFrame(s, string(rune('a'+i%26))) })
	waitHighlights(s) // the last reparse done before the scene closes
	var total time.Duration
	for _, d := range times {
		total += d
	}
	mean := total / keys
	t.Logf("a typing frame: mean %v, p99 %v", mean, perf.Pct(times, 99))
	if mean > 2*time.Millisecond {
		t.Errorf("a typing frame takes %v on average; the budget is 2ms", mean)
	}
}

// Opening a file doesn't hold up the frame: its parser (the grammar's wasm) is made off the loop, and the file
// shows plain until it's ready.
func TestOpeningAFileFrameBudget(t *testing.T) {
	perf.Timed(t)
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing needs a build without the race detector or coverage")
	}
	dir := t.TempDir()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		os.WriteFile(filepath.Join(dir, f), []byte(rig.RoundGo()), 0o644)
	}
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: apptest.MockupSize})
	s.Frame()
	for _, f := range []string{"a.go", "b.go", "c.go"} {
		s.Run("editor.open", registry.Args{"path": f, "place": "split"})
		var sm perf.Sampler
		sm.Time(func() { s.Frame() })
		if d := sm.Times()[0]; d > 30*time.Millisecond {
			t.Errorf("the frame after opening %s took %v", f, d)
		}
	}
	waitHighlights(s)
}
