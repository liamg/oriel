package editor_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/editor"
	"github.com/liamg/oriel/registry"
)

// editor.open with a line puts the cursor on it, well inside the view, however far down a long file it is and
// however tall the window it opens in: a tab, or a split half the height.
func TestEditorOpenLandsOnTheLine(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&b, "// line %d\n", i)
	}
	app := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Write: map[string]string{"long.go": b.String(), "other.go": "package p\n"}})
	x := apptest.WindowPlugin[*editor.Editor](app, "editor")
	open := func(file string, line int, place string) {
		t.Helper()
		if err := app.Do(func() error {
			return app.Host.Run(context.Background(), "editor.open", registry.Args{"path": filepath.Join(app.Dir, file), "line": line, "place": place})
		}); err != nil {
			t.Fatal(err)
		}
	}
	jump := func(line int, place string) {
		t.Helper()
		open("long.go", line, place)
		var cur, top, rows int
		deadline := time.Now().Add(apptest.Timeout)
		for {
			app.Do(func() error {
				p := x.Current()
				cur, top, rows = p.Cursor().Line, p.Top(), x.Rows(p.ID())
				return nil
			})
			at := cur - top
			if rows > 0 && cur == line-1 && at >= rows/4 && at <= rows*3/4 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("opened at line %d (%s), the cursor is on line %d, row %d of %d: want it on that line, near the middle", line, place, cur+1, at, rows)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	open("other.go", 1, "")
	app.WaitText("package p")
	jump(300, "split")
	jump(40, "")
	jump(550, "")
	jump(200, "split")
}
