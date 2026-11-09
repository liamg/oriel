package cli

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// Once the window has closed, the process ends within exitAfter whatever is stuck, and logs every goroutine's stack
// as it does; nothing ends it before then.
func TestAClosedWindowsProcessEndsWithinABound(t *testing.T) {
	clk := clocktest.New(time.Unix(1000, 0))
	var logged, stderr strings.Builder
	code := -1
	e := &exitWatch{clock: clk, after: exitAfter, log: slog.New(slog.NewTextHandler(&logged, nil)), stderr: &stderr,
		exit: func(c int) { code = c }}
	e.start()
	e.start() // a second close changes nothing
	clk.Advance(exitAfter - time.Millisecond)
	if code != -1 {
		t.Fatalf("exited with %d before the bound", code)
	}
	clk.Advance(time.Millisecond)
	if code != 1 {
		t.Fatalf("exit code %d at the bound, want 1", code)
	}
	if !strings.Contains(logged.String(), "goroutine ") || !strings.Contains(logged.String(), "TestAClosedWindowsProcessEndsWithinABound") {
		t.Errorf("the log has no stacks: %.300q", logged.String())
	}
	if !strings.Contains(stderr.String(), "exiting") {
		t.Errorf("stderr: %q", stderr.String())
	}
	if clk.Waiting() != 0 {
		t.Errorf("%d timers left", clk.Waiting())
	}
}
