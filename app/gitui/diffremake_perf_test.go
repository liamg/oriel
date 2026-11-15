//go:build perf

package gitui_test

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// A changed file open in the diff view and written to over and over, by the wall clock, has its diff remade twice a
// second at most, not on every write, and the diff still catches up with the last write (the pacing alone is
// TestDiffRemakesArePaced, on a clock of its own).
func TestDiffRemakePaceBudget(t *testing.T) {
	s, r := explorerScene(t)
	s.Logs.Level.Set(slog.LevelDebug)
	const path = "internal/pricing/round.go"
	s.Run("diff.open", registry.Args{"path": filepath.Join(r.Dir, path)})
	newText := func() ([]byte, bool) { return diffOf(s).NewText(path) }
	if !s.Headless.Until(5*time.Second, func() bool { _, ok := newText(); return ok }) {
		t.Fatal("round.go's diff was never made")
	}
	file := filepath.Join(r.Dir, path)
	last := ""
	start := time.Now()
	for i := 0; time.Since(start) < 2*time.Second; i++ {
		last = fmt.Sprintf("// write %d", i)
		os.WriteFile(file, []byte("package pricing\n\nfunc Round() {}\n"+last+"\n"), 0o644)
		s.Quiet()
		time.Sleep(20 * time.Millisecond) // the pace of the writes
	}
	remakes := 0
	for _, rec := range s.Logs.Ring.Records() {
		if rec.Msg == "remaking a diff" && strings.Contains(rec.Text(), path) {
			remakes++
		}
	}
	if remakes > 5 || remakes == 0 {
		t.Errorf("2s of writes every 20 ms remade the diff %d times; want 1 to 5", remakes)
	}
	if !s.Headless.Until(5*time.Second, func() bool { b, ok := newText(); return ok && bytes.Contains(b, []byte(last)) }) {
		t.Errorf("the diff never caught up with the last write, %q", last)
	}
}
