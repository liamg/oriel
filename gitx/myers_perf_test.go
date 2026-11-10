//go:build perf

package gitx

import (
	"runtime"
	"testing"
)

// A new file of 20,000 lines diffs without allocating a trace for it, and a rewrite past myersMaxD edits is a
// replacement rather than gigabytes of trace.
func TestMyersMemoryBudget(t *testing.T) {
	for _, tt := range myersLong() {
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		myers(tt.a, tt.b)
		runtime.ReadMemStats(&after)
		if mb := (after.TotalAlloc - before.TotalAlloc) >> 20; mb > 64 {
			t.Errorf("%s allocated %d MB", tt.name, mb)
		}
	}
}
