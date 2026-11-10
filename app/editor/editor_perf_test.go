//go:build perf && !race

package editor_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/tools/perf"
)

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
func TestEditorTypingFrameAllocsBudget(t *testing.T) {
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
