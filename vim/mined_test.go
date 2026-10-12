package vim_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvim"
)

// TestMined runs the cases mined from vim's tests (see vimtest/mine). Every case must pass, as the
// hand-written ones must; one we choose not to match says so with deviation:, which is logged instead.
// In oracle mode every case must also match nvim.
func TestMined(t *testing.T) {
	for _, c := range vimtest.Load(t, "testdata/mined/*.vt") {
		t.Run(c.Name, func(t *testing.T) {
			c.RequireRef(t)
			if vimtest.Oracle() {
				got := vimtest.Run(t, nvim.New(), c)
				if c.Follows == "vim" || c.Deviation != "" {
					for _, d := range c.Diff(got) {
						t.Logf("%s (allowed: follows=%q deviation=%q): %s", got.Source, c.Follows, c.Deviation, d)
					}
				} else {
					c.Compare(t, got)
				}
			}
			for _, d := range runQuietly(c) {
				if c.Deviation != "" {
					t.Logf("engine (deviation: %s): %s", c.Deviation, oneLine(d))
				} else {
					t.Errorf("%s:%d: engine: %s", c.File, c.Line, d)
				}
			}
		})
	}
}

// runQuietly runs a case on the engine and returns its diffs, turning a fatal error, a panic or a hang
// into a diff instead of failing the test.
func runQuietly(c vimtest.Case) (diffs []string) {
	done := make(chan []string, 1)
	go func() {
		qt := &quietTB{}
		defer func() {
			if r := recover(); r != nil {
				if qt.fatal != "" {
					done <- []string{qt.fatal}
				} else {
					done <- []string{fmt.Sprintf("panic: %v", r)}
				}
			}
		}()
		done <- c.Diff(vimtest.Run(qt, newTestEngine, c))
	}()
	select {
	case d := <-done:
		return d
	case <-time.After(5 * time.Second):
		return []string{"timed out"}
	}
}

// quietTB is a testing.TB whose Fatal panics, so a case's fatal error can be recovered from.
type quietTB struct {
	testing.TB
	fatal string
}

func (q *quietTB) Helper() {}

func (q *quietTB) Fatalf(format string, args ...any) {
	q.fatal = fmt.Sprintf(format, args...)
	panic(q.fatal)
}

func (q *quietTB) Fatal(args ...any) {
	q.fatal = fmt.Sprint(args...)
	panic(q.fatal)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
