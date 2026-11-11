package gitui_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// options.txt *'wrap'*: "When on, lines longer than the width of the window will wrap and displaying continues on the
// next line." The diff follows it as the editor does: a long line takes as many rows as it needs, split or unified,
// and is whole across them; with 'nowrap' it takes one.
func TestDiffWrapsLongLines(t *testing.T) {
	long := "start " + strings.Repeat("middle ", 60) + "finish"
	for _, layout := range []string{"split", "unified"} {
		t.Run(layout, func(t *testing.T) {
			r := gittest.New(t)
			r.Write(map[string]string{"notes.txt": "first\n"})
			r.Commit("Ada", "base")
			r.Write(map[string]string{"notes.txt": "first\n" + long + "\n"})
			s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 800}})
			opts := s.Registry().Options()
			if err := opts.Set("diff.layout", registry.LayerUser, registry.At{}, layout); err != nil {
				t.Fatal(err)
			}
			s.Run("diff.open", registry.Args{"path": r.Dir})
			s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "finish") || strings.Contains(s.Text(), "middle") })
			s.Quiet()
			if n := len(rowsOf(s, long)); n != 1 {
				t.Fatalf("with nowrap the line takes %d rows", n)
			}
			if err := opts.Set("editor.wrap", registry.LayerUser, registry.At{}, true); err != nil {
				t.Fatal(err)
			}
			s.Quiet()
			rs := rowsOf(s, long)
			if len(rs) < 2 {
				t.Fatalf("with wrap the line takes %d rows", len(rs))
			}
			joined := ""
			for i, op := range rs {
				joined += op.text
				if i > 0 && op.y <= rs[i-1].y {
					t.Errorf("row %d isn't below row %d", i, i-1)
				}
			}
			if joined != long {
				t.Errorf("the rows don't make up the line:\n%q", joined)
			}
			if !strings.Contains(s.Text(), "finish") {
				t.Error("the line's end is cut off")
			}
		})
	}
}

type row struct {
	y    float32
	text string
}

// rowsOf are the screen rows the text of line is drawn on, top to bottom: each row's runs joined, a cut one with its "…".
func rowsOf(s *apptest.Scene, line string) []row {
	at := map[float32]string{}
	for _, op := range s.List().Ops {
		if op.Kind != render.OpGlyphs {
			continue
		}
		t := strings.TrimSuffix(op.Run.Text, "…")
		if t != "" && strings.Contains(line, t) && (strings.Contains(t, "middle") || strings.Contains(t, "start") || strings.Contains(t, "finish")) {
			at[op.At.Y] += op.Run.Text
		}
	}
	var out []row
	for y, t := range at {
		out = append(out, row{y, t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].y < out[j].y })
	return out
}
