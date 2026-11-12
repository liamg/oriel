package gitui_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// jumpRepo is a branch changing every 15th line of n files of 300 lines: long files of many hunks.
func jumpRepo(t *testing.T, n int) *gittest.Repo {
	r := gittest.New(t)
	base, changed := map[string]string{"go.mod": "module x\n"}, map[string]string{}
	for f := range n {
		var b, c strings.Builder
		for i := 1; i <= 300; i++ {
			fmt.Fprintf(&b, "// f%02d line %d\n", f, i)
			if i%15 == 0 {
				fmt.Fprintf(&c, "// f%02d line %d changed\n", f, i)
			} else {
				fmt.Fprintf(&c, "// f%02d line %d\n", f, i)
			}
		}
		name := fmt.Sprintf("f%02d.go", f)
		base[name], changed[name] = b.String(), c.String()
	}
	r.Write(base)
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "work")
	r.Write(changed)
	return r
}

// centred says the line reading text is selected, in the middle half of the diff's list of changes.
func centred(w *apptest.Window, text string) bool {
	list, row := w.Access("Changes"), w.Access(text)
	if list == nil || row == nil || !row.Selected {
		return false
	}
	b, l := row.Bounds, list.Bounds
	return b.Dy() > 0 && b.Min.Y >= l.Min.Y+l.Dy()/4 && b.Max.Y <= l.Max.Y-l.Dy()/4
}

// diff.open with a line lands on that line: selected and in the middle of the view, however far down its file and the files above
// it it is, and whatever was open and scrolled before.
func TestDiffOpenLandsOnTheLine(t *testing.T) {
	r := jumpRepo(t, 12)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	jump := func(file string, line int, text string) {
		t.Helper()
		if err := app.Do(func() error {
			return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, file), "line": line})
		}); err != nil {
			t.Fatal(err)
		}
		app.Latest(text+" selected in the middle of the view", func(_ headless.Frame) bool { return centred(app, text) })
	}
	jump("f00.go", 30, "// f00 line 30 changed")
	jump("f00.go", 285, "// f00 line 285 changed")
	jump("f09.go", 240, "// f09 line 240 changed")
	jump("f03.go", 150, "// f03 line 150 changed")
	jump("f09.go", 15, "// f09 line 15 changed")
	jump("f11.go", 150, "// f11 line 150 changed")
}

// shows says the line reading text is drawn inside the diff's list of changes.
func shows(w *apptest.Window, text string) bool {
	list, row := w.Access("Changes"), w.Access(text)
	return list != nil && row != nil && row.Bounds.Dy() > 0 && row.Bounds.Min.Y >= list.Bounds.Min.Y && row.Bounds.Max.Y <= list.Bounds.Max.Y
}

// Opening a file's diff (diff.open, as a click on it in the explorer does) shows that file expanded, from its first
// hunk: collapsed or not, far down or not.
func TestDiffOpenExpandsTheFile(t *testing.T) {
	r := jumpRepo(t, 12)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	if err := app.Do(func() error {
		return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, "f00.go"), "line": 285})
	}); err != nil {
		t.Fatal(err)
	}
	app.Latest("f00.go's line 285", func(headless.Frame) bool { return centred(app, "// f00 line 285 changed") })
	open := func(f string) {
		app.T.Helper()
		if err := app.Do(func() error {
			return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, f)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"f09", "f03", "f09", "f11", "f00"} {
		open(f + ".go")
		first := "// " + f + " line 15 changed"
		app.Latest(f+".go expanded from its first hunk", func(headless.Frame) bool { return shows(app, first) })
	}
	// Folded by its header, opening it again unfolds it.
	app.ClickAccessIn("Changes", "f00.go")
	app.WaitGone("// f00 line 15 changed")
	open("f00.go")
	app.Latest("f00.go expanded again", func(headless.Frame) bool { return shows(app, "// f00 line 15 changed") })
}

// diff.open's line on the old side (a comment on removed code) lands on the removed line.
func TestDiffOpenLandsOnAnOldLine(t *testing.T) {
	r := jumpRepo(t, 6)
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: r.Dir})
	app.Do(func() error {
		return app.Host.Run(context.Background(), "diff.layout", registry.Args{"layout": "unified"})
	})
	if err := app.Do(func() error {
		return app.Host.Run(context.Background(), "diff.open", registry.Args{"path": filepath.Join(r.Dir, "f04.go"), "line": 210, "side": "old"})
	}); err != nil {
		t.Fatal(err)
	}
	app.Latest("the removed line 210 selected in the middle of the view", func(headless.Frame) bool { return centred(app, "// f04 line 210") })
}
