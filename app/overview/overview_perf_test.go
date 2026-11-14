//go:build perf && !race

package overview_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/overview"
	"github.com/liamg/oriel/tools/perf"
)

// On a 100k-file repo the overview shows at once, and frames stay under a frame's budget while it counts.
func TestOverviewBigRepoBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("makes 100,000 files")
	}
	dir := filepath.Join(t.TempDir(), "big")
	files := map[string]string{}
	for i := range 100_000 {
		files[fmt.Sprintf("pkg%03d/f%05d.go", i%500, i)] = "package p\n\nfunc f() {}\n"
	}
	gitRepo(t, dir, files, 3)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.OverviewOff.Dispose()
	var frames perf.Sampler
	deadline := time.Now().Add(overview.PanelBudget + 5*time.Second)
	for !overviewDone(s) {
		frames.Time(func() {
			s.RunQueued()
			s.Frame()
		})
		if time.Now().After(deadline) {
			t.Fatalf("not done within the panels' budget:\n%s", s.Text())
		}
		time.Sleep(2 * time.Millisecond)
	}
	times := frames.Times()
	slices.Sort(times)
	p95, worst := times[len(times)*95/100], times[len(times)-1]
	t.Logf("frames while counting: %d, p95 %v, slowest %v", len(times), p95, worst)
	// A frame's time is the loop thread's own (perf.Sampler), so the counting beside it on every core isn't; counting
	// on the loop would slow most frames, and one far more.
	if p95 > 16*time.Millisecond || worst > 50*time.Millisecond {
		t.Errorf("frames took p95 %v, slowest %v, while the overview counted; the budget is 16 ms", p95, worst)
	}
	if !strings.Contains(s.Text(), "100,000") {
		t.Errorf("doesn't count 100,000 files:\n%s", s.Text())
	}
}
