// Package leaktest fails a test that leaves goroutines running once its cleanups are done.
package leaktest

import (
	"bytes"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Grace is how long a test's goroutines get to end after its cleanups.
const Grace = 3 * time.Second

// ignored are frames of goroutines the runtime and the test framework run, not a test's.
var ignored = []string{
	"testing.(*T).Run(",
	"testing.(*M).",
	"testing.runTests",
	"testing.tRunner(",
	"os/signal.signal_recv",
	"os/signal.loop",
	"runtime.ensureSigM",
	"runtime/pprof.",
}

var checked sync.Map // test name → true, while that test runs

// Check notes the goroutines running now; when t ends, after every cleanup registered later (so after the app,
// window or host the test started is closed), it fails t with the stacks of the goroutines started since that are
// still running after Grace. A second call in the same test does nothing.
func Check(t testing.TB) {
	t.Helper()
	if _, dup := checked.LoadOrStore(t.Name(), true); dup {
		return
	}
	before := map[int]bool{}
	for _, g := range goroutines() {
		before[g.id] = true
	}
	t.Cleanup(func() {
		defer checked.Delete(t.Name())
		if t.Failed() || t.Skipped() {
			return
		}
		if left := Leaked(before, Grace); len(left) > 0 {
			t.Errorf("%d goroutines still running after the test's cleanups:\n\n%s", len(left), strings.Join(left, "\n\n"))
		}
	})
}

// Leaked waits up to grace for every goroutine not in before to end, and answers the stacks of those that didn't.
func Leaked(before map[int]bool, grace time.Duration) []string {
	deadline := time.Now().Add(grace)
	for {
		var left []string
		for _, g := range goroutines() {
			if !before[g.id] && !framework(g.stack) {
				left = append(left, g.stack)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			return left
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type goroutine struct {
	id    int
	stack string
}

func goroutines() []goroutine {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []goroutine
	for _, g := range bytes.Split(buf, []byte("\n\n")) {
		s := string(g)
		head, _, _ := strings.Cut(s, "\n")
		f := strings.Fields(head)
		if len(f) < 2 || f[0] != "goroutine" {
			continue
		}
		id, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		out = append(out, goroutine{id, s})
	}
	return out
}

// framework says the goroutine is the runtime's or the test framework's: a C thread's, back in C, has no Go frames.
func framework(stack string) bool {
	return cThread(stack) || slices.ContainsFunc(ignored, func(f string) bool { return strings.Contains(stack, f) })
}

// cThread says the goroutine is the one a C thread that called into Go keeps: locked to it, and only runtime.goexit.
func cThread(stack string) bool {
	head, rest, _ := strings.Cut(strings.TrimSpace(stack), "\n")
	fn, _, _ := strings.Cut(rest, "\n")
	return strings.Contains(head, "locked to thread") && strings.HasPrefix(fn, "runtime.goexit(") &&
		strings.Count(rest, "\n") <= 1
}
