package vim_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/vim/vimtest"
	"github.com/liamg/oriel/vim/vimtest/nvim"
)

// minedPassRate is the share of mined cases (vim's own tests, see vimtest/mine) the engine must pass.
const minedPassRate = 0.90

// TestMined runs the cases mined from vim's tests. A failing case doesn't fail the test; the pass rate
// falling below minedPassRate does. In oracle mode every case must also match nvim.
func TestMined(t *testing.T) {
	cases := vimtest.Load(t, "testdata/mined/*.vt")
	var failed []string
	for _, c := range cases {
		c.RequireRef(t)
		if vimtest.Oracle() {
			t.Run(c.Name, func(t *testing.T) {
				got := vimtest.Run(t, nvim.New(), c)
				if c.Follows == "vim" || c.Deviation != "" {
					for _, d := range c.Diff(got) {
						t.Logf("%s (allowed: follows=%q deviation=%q): %s", got.Source, c.Follows, c.Deviation, d)
					}
				} else {
					c.Compare(t, got)
				}
			})
		}
		if diffs := runQuietly(c); len(diffs) > 0 {
			failed = append(failed, fmt.Sprintf("%s (%s:%d): %s", c.Name, c.File, c.Line, oneLine(diffs[0])))
		}
	}
	passed := len(cases) - len(failed)
	rate := float64(passed) / float64(len(cases))
	t.Logf("mined: %d/%d cases pass (%.1f%%)", passed, len(cases), 100*rate)
	for _, f := range failed {
		t.Logf("  FAIL %s", f)
	}
	if rate < minedPassRate {
		t.Errorf("mined pass rate %.1f%% is below %.0f%%", 100*rate, 100*minedPassRate)
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
