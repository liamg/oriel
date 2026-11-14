package gitui_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

// A changed file open in the diff view and written to over and over has its diff remade twice a second at most, not on every write, and the
// diff still catches up with the last write.
func TestDiffRemadeAtAPaceWhileWritten(t *testing.T) {
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

// A line diff.open selected stays selected when the diff is made again with other hunks: here the status read as it
// opened is out of date (the file still new), and the next read finds its base.
func TestDiffSelectedLineSurvivesARemake(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"go.mod": "module example.com/pricing\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "pricing-rounding")
	r.Write(map[string]string{"round.go": rig.RoundAgent})
	app := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: r.Dir})
	path := filepath.Join(r.Dir, "round.go")
	if err := app.Do(func() error {
		return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": path, "line": 8})
	}); err != nil {
		t.Fatal(err)
	}
	selected := func(app *apptest.Window) bool {
		n := app.Access("\tt := scaled.Truncate(0)")
		return n != nil && n.Selected
	}
	app.WaitFrame("the new file's diff", func(s string) bool { return strings.Contains(s, "@@ -0,0 +1,") })
	app.WaitAccess("line 8 selected in the new file", selected)

	r.Write(map[string]string{"round.go": rig.RoundBase})
	r.Commit("Ada", "round")
	r.Write(map[string]string{"round.go": rig.RoundAgent})
	r.Git("update-ref", "refs/heads/main", "HEAD")
	app.Do(func() error { return app.Host.Run(context.Background(), "git.refresh", nil) })
	app.WaitFrame("the diff against its base", func(s string) bool { return strings.Contains(s, "@@ -5,8 +5,12 @@") })
	app.WaitAccess("line 8 still selected", selected)
}
