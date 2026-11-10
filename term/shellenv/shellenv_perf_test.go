//go:build perf

package shellenv

import (
	"testing"
	"time"

	"github.com/liamg/oriel/internal/isolate"
)

// A shell that starts something that never ends is given up on soon after the timeout.
func TestPathGivesUpOnAShellThatHangsBudget(t *testing.T) {
	isolate.Test(t)
	sh, _ := hangingShell(t)
	start := time.Now()
	Path(t.Context(), sh, 300*time.Millisecond)
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("gave up after %v", d)
	}
}

// A shell that goes on writing after the PATH is let go of in time.
func TestPathFromAShellThatKeepsWritingBudget(t *testing.T) {
	isolate.Test(t)
	sh := chattyShell(t)
	start := time.Now()
	Path(t.Context(), sh, Timeout)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v to let the shell go", d)
	}
}
