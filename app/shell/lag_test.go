//go:build !race

package shell_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/tools/perf"
)

// omniboxDownFrame is the average frame that shows ↓ moving the omnibox's selection, over dir's n files.
func omniboxDownFrame(t *testing.T, dir string, n int) time.Duration {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.WaitGit()
	sourcesLoaded(s)
	s.Run("omnibox.open", registry.Args{"query": "pkg"})
	for range 10 {
		s.Frame()
	}
	took := perf.Mean(perf.Times(3, 100, nil, func(int) {
		s.Key("Down")
		s.Frame()
	}))
	if got := omni(s).Selected(); got == 0 {
		t.Fatalf("↓ over %d files never moved the selection", n)
	}
	return took
}

// ↓ in the omnibox doesn't grow with the files it searches: the ranking is kept while the selection moves.
func TestOmniboxDownWithManyFilesBudget(t *testing.T) {
	perf.Timed(t)
	few, many := omniboxDownFrame(t, rig.ChangedRepo(t, 5), 5), omniboxDownFrame(t, rig.ChangedRepo(t, 4000), 4000)
	t.Logf("↓ in the omnibox over 5 files: %v; over 4000: %v", few, many)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("4000 files make ↓ in the omnibox take %v, against %v over 5", many, few)
	}
}

// omniboxTypingFrame is the average frame that shows a key typed into the omnibox's query, over dir's n files, and
// how long until the files matching the whole query show.
func omniboxTypingFrame(t *testing.T, dir string, n int) (frame, shown time.Duration) {
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.WaitGit()
	sourcesLoaded(s)
	s.Run("omnibox.open", nil)
	for range 10 {
		s.Frame()
	}
	start := time.Now()
	keys := strings.Split("pkg1f099", "")
	frame = perf.Mean(perf.Times(1, len(keys), nil, func(i int) {
		s.Type(keys[i])
		s.Frame()
	}))
	s.Until(fmt.Sprintf("pkg19/f099.txt in the omnibox over %d files", n), func() bool { return strings.Contains(s.Text(), "pkg19/f099.txt") })
	return frame, time.Since(start)
}

// Typing into the omnibox doesn't wait on ranking its files: they're matched off the loop, and the files matching
// the query show soon after.
func TestOmniboxTypingWithManyFilesBudget(t *testing.T) {
	perf.Timed(t)
	few, _ := omniboxTypingFrame(t, rig.ChangedRepo(t, 100), 100)
	many, shown := omniboxTypingFrame(t, rig.ChangedRepo(t, 20000), 20000)
	t.Logf("a key typed into the omnibox over 100 files: %v; over 20000: %v, the matches shown after %v", few, many, shown)
	if many > 2*few+2*time.Millisecond {
		t.Errorf("20000 files make a key typed into the omnibox take %v, against %v over 100", many, few)
	}
}
