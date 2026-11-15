package gitui_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
)

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
