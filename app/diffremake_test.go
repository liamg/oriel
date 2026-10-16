package app

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A changed file written to over and over has its diff remade twice a second at most, not on every write, and the
// diff still catches up with the last write.
func TestDiffRemadeAtAPaceWhileWritten(t *testing.T) {
	s, r := explorerScene(t)
	s.sh.cfg.Logs.Level.Set(slog.LevelDebug)
	s.keyCommand(t, "<M-1>")
	const path = "internal/pricing/round.go"
	diffOf := func() *diffFile {
		x := s.diff()
		x.mu.Lock()
		defer x.mu.Unlock()
		for k, d := range x.files {
			if k.path == path {
				return d
			}
		}
		return nil
	}
	if !s.Until(5*time.Second, func() bool { return diffOf() != nil }) {
		t.Fatal("round.go's diff was never made")
	}
	file := filepath.Join(r.Dir, path)
	last := ""
	start := time.Now()
	for i := 0; time.Since(start) < 2*time.Second; i++ {
		last = fmt.Sprintf("// write %d", i)
		os.WriteFile(file, []byte("package pricing\n\nfunc Round() {}\n"+last+"\n"), 0o644)
		s.frame()
		s.RunQueued()
		time.Sleep(20 * time.Millisecond)
	}
	remakes := 0
	for _, rec := range s.sh.cfg.Logs.Ring.Records() {
		if rec.Msg == "remaking a diff" && strings.Contains(rec.Text(), path) {
			remakes++
		}
	}
	if remakes > 5 || remakes == 0 {
		t.Errorf("2s of writes every 20 ms remade the diff %d times; want 1 to 5", remakes)
	}
	if !s.Until(5*time.Second, func() bool { d := diffOf(); return d != nil && bytes.Contains(d.newText, []byte(last)) }) {
		t.Errorf("the diff never caught up with the last write, %q", last)
	}
}
