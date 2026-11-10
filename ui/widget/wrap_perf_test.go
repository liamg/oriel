//go:build perf

package widget

import (
	"strings"
	"testing"
	"time"
)

// A long token wraps in under 50 ms.
func TestWrapLinesLongTokenBudget(t *testing.T) {
	ts := bundled(t)
	for _, n := range []int{4000, 10000} {
		tok := strings.Repeat("QmFzZTY0IGJsb2Ig", n/16+1)[:n]
		start := time.Now()
		WrapLines(ts, tok, 400, wrapFont)
		d := time.Since(start)
		t.Logf("%d chars: %v", n, d)
		if d > 50*time.Millisecond {
			t.Errorf("a %d-char token took %v to wrap", n, d)
		}
	}
}
