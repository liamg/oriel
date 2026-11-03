package editor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// With 'nowrap' a wide line builds only the text the window shows, from its left column to its right edge.
func TestNowrapBuildsOnlyTheVisibleText(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "wide.txt"), []byte("short\n"+strings.Repeat("x", 20_000)+"END\n"), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir, Size: render.Point{X: 1200, Y: 600}})
	s.Run("editor.open", registry.Args{"path": "wide.txt"})
	s.Frame()
	keys(s, ":set nowrap<CR>")
	keys(s, "j")
	for _, at := range []string{"at the start", "scrolled to the end"} {
		var widest int
		for _, l := range strings.Split(s.FullText(), "\n") {
			widest = max(widest, strings.Count(l, "x"))
		}
		p, _ := engine(s).Pane(engine(s).WindowID())
		if cols := p.Cols(); widest == 0 || widest > cols+1 {
			t.Errorf("%s: built %d of the line's columns, with %d in view", at, widest, cols)
		}
		keys(s, "$")
	}
	if !strings.Contains(s.FullText(), "END") {
		t.Error("scrolled to the end, END isn't drawn")
	}
}
