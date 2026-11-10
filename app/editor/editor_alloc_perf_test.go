//go:build perf

package editor_test

import (
	"runtime"
	"testing"
)

// The per-frame target on BenchmarkEditorTyping: a typing frame allocates under 0.6MB. The count is the process's,
// so what else runs alongside moves it: it runs with make perf.
func TestEditorTypingFrameAllocBudget(t *testing.T) {
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
