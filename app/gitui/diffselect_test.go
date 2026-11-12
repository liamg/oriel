package gitui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// rowFilled says a rect spanning most of node's row is filled: a selection's or a change's fill, not just its parts'.
func rowFilled(s *apptest.Scene, n *render.AccessNode) bool {
	b := n.Bounds
	for _, op := range s.List().Ops {
		if op.Kind == render.OpRect && op.Style.Fill.A > 0 && op.Rect.Dx() >= b.Dx()*0.9 &&
			op.Rect.Min.Y >= b.Min.Y-.5 && op.Rect.Max.Y <= b.Max.Y+.5 {
			return true
		}
	}
	return false
}

// A click on a diff's line selects it, shows it, and starts a comment on it (thread.new, whose composer offers who
// it's for): the row is filled and edged in the accent, and the diff's foot says where it is and the keys that act on
// it. A right-click selects the line too, and its menu opens it in the file, comments, asks, explains or copies where
// it is.
func TestADiffLineClickCommentsOnIt(t *testing.T) {
	for _, layout := range []string{"split", "unified"} {
		t.Run(layout, func(t *testing.T) {
			r := gittest.New(t)
			var base strings.Builder
			for i := 1; i <= 30; i++ {
				fmt.Fprintf(&base, "line %d\n", i)
			}
			r.Write(map[string]string{"f.txt": base.String()})
			r.Commit("Ada", "base")
			path := filepath.Join(r.Dir, "f.txt")
			os.WriteFile(path, []byte(strings.Replace(base.String(), "line 15\n", "line 15, changed\n", 1)), 0o644)
			s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
			if err := s.Registry().Options().Set("diff.layout", registry.LayerUser, registry.At{}, layout); err != nil {
				t.Fatal(err)
			}
			s.Run("diff.open", registry.Args{"path": path})
			s.Until("the diff", func() bool { return s.Find("line 13") != nil })
			s.Quiet()

			if rowFilled(s, s.Node("line 13")) || s.Find("Keys for the selected line") != nil {
				t.Fatal("an unselected context line is filled, or keys show with nothing selected")
			}
			// The threads' own composer is TestDiffCommentOpensAThreadBetweenTheLines's; here, what's asked of it.
			var asked registry.Args
			s.Registry().Command(registry.Command{ID: "thread.new", Run: func(_ registry.Ctx, a registry.Args) error { asked = a; return nil }})
			s.ClickOn("line 13")
			s.Quiet()
			n := s.Node("line 13")
			if !n.Selected || !rowFilled(s, n) {
				t.Errorf("clicked, line 13 is selected %v, filled %v", n.Selected, rowFilled(s, n))
			}
			if s.Find("Keys for the selected line") == nil || !strings.Contains(s.Text(), "f.txt:13") || !strings.Contains(s.Text(), "open") {
				t.Error("the selected line doesn't say its keys")
			}
			if asked == nil || asked.String("file") != path || asked.Int("line") != 13 || asked.String("to") != "" {
				t.Errorf("the click asked thread.new for %v, want a comment on f.txt line 13, for whoever the composer says", asked)
			}

			asked = nil
			menu := s.RightClick(apptest.Centre(s.Node("line 16").Bounds))
			for _, want := range []string{"Open in file", "Comment…", "Ask the agent…", "Explain", "Copy reference"} {
				if !strings.Contains(menu, want) {
					t.Errorf("the line's menu lacks %q", want)
				}
			}
			if !s.Node("line 16").Selected {
				t.Error("the right-click didn't select line 16")
			}
			s.ClickTop("Ask the agent…")
			s.Quiet()
			if asked == nil || asked.Int("line") != 16 || asked.String("to") != "agent" {
				t.Errorf("Ask the agent… asked thread.new for %v, want the agent, on line 16", asked)
			}
			s.RightClick(apptest.Centre(s.Node("line 17").Bounds))
			s.ClickTop("Open in file")
			s.Until("line 17 in the editor", func() bool { return s.Showing(kit.EditorView) && strings.Contains(s.Text(), "17:1") })
		})
	}
}
