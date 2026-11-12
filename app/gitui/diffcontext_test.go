package gitui_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// contextFile is a 60-line file and its change: line 10 made two lines, line 50 changed. Its diff has two hunks,
// new lines 7–14 and 48–54, with new lines 1–6, 15–47 (old 14–46) and 55–61 (old 54–60) hidden around them. Its
// lines are indented, so no hunk header names one as the function it's in.
func contextFile() (old, new string) {
	var o, n strings.Builder
	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&o, "  keep %02d\n", i)
		switch i {
		case 10:
			n.WriteString("  CHANGED 10a\n  CHANGED 10b\n")
		case 50:
			n.WriteString("  CHANGED 50\n")
		default:
			fmt.Fprintf(&n, "  keep %02d\n", i)
		}
	}
	return o.String(), n.String()
}

func contextScene(t *testing.T, layout string) (*apptest.Scene, *gittest.Repo) {
	t.Helper()
	old, new := contextFile()
	r := gittest.New(t)
	r.Write(map[string]string{"lines.txt": old})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"lines.txt": new})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 2400}})
	if err := s.Registry().Options().Set("diff.layout", registry.LayerUser, registry.At{}, layout); err != nil {
		t.Fatal(err)
	}
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "CHANGED 50") })
	s.Quiet()
	return s, r
}

// keeps are which of keep 01–60 the frame shows.
func keeps(s *apptest.Scene) map[int]bool {
	out := map[int]bool{}
	text := s.Text()
	for i := 1; i <= 60; i++ {
		if strings.Contains(text, fmt.Sprintf("keep %02d", i)) {
			out[i] = true
		}
	}
	return out
}

// wantKeeps fails unless the frame shows exactly the keep lines in the ranges given, old numbers, inclusive, and the
// removed lines 10 and 50.
func wantKeeps(t *testing.T, s *apptest.Scene, what string, ranges ...[2]int) {
	t.Helper()
	want := map[int]bool{10: true, 50: true}
	for _, r := range ranges {
		for i := r[0]; i <= r[1]; i++ {
			want[i] = true
		}
	}
	got := keeps(s)
	for i := 1; i <= 60; i++ {
		if got[i] != want[i] {
			t.Errorf("%s: keep %02d shown %v, want %v", what, i, got[i], want[i])
		}
	}
}

// numbersBeside are the numbers drawn on the row of an indented line's text, left to right.
func numbersBeside(s *apptest.Scene, text string) []string {
	at, ok := s.TextStart("  " + text)
	if !ok {
		return nil
	}
	var out []string
	for _, op := range s.List().Ops {
		if op.Kind != render.OpGlyphs || op.At.Y != at.Y || op.At.X >= at.X {
			continue
		}
		if _, err := fmt.Sscan(op.Run.Text, new(int)); err == nil {
			out = append(out, op.Run.Text)
		}
	}
	return out
}

// GitHub's diff hides the unchanged lines around its hunks behind expanders: one above the first hunk that shows
// lines up from it, one below the last that shows lines down, and between two hunks one of each, 20 lines a click,
// until what's left is 20 or fewer, which one expander shows at once. The lines shown are the file's, numbered on
// both sides as their place in the old and new file says, and stay shown while the diff is the same.
func TestDiffShowsHiddenLinesAroundHunks(t *testing.T) {
	s, _ := contextScene(t, "unified")
	// git's 3 lines of context: old 7–9, 11–13 and 47–49, 51–53.
	wantKeeps(t, s, "at first", [2]int{7, 9}, [2]int{11, 13}, [2]int{47, 49}, [2]int{51, 53})

	// Between the hunks, 20 lines down from the hunk above: new 15–34, old 14–33.
	s.ClickOn("Show lines 15 to 34")
	s.Quiet()
	wantKeeps(t, s, "20 down from the first hunk", [2]int{7, 9}, [2]int{11, 33}, [2]int{47, 49}, [2]int{51, 53})
	if got := numbersBeside(s, "keep 20"); strings.Join(got, " ") != "20 21" {
		t.Errorf("keep 20 is numbered %v, want old 20 and new 21", got)
	}
	// 13 left, 20 or fewer: one expander shows them all.
	if s.Find("Show lines 28 to 47") != nil {
		t.Error("the gap of 13 still offers 20 lines up")
	}
	s.ClickOn("Show lines 35 to 47")
	s.Quiet()
	wantKeeps(t, s, "the gap between the hunks closed", [2]int{7, 9}, [2]int{11, 49}, [2]int{51, 53})
	if s.Find("Show lines 35 to 47") != nil {
		t.Error("a closed gap still has an expander")
	}

	// Above the first hunk, up from it; below the last, down to the file's end.
	s.ClickOn("Show lines 1 to 6")
	s.ClickOn("Show lines 55 to 61")
	s.Quiet()
	wantKeeps(t, s, "every gap closed", [2]int{1, 9}, [2]int{11, 49}, [2]int{51, 60})
	if got := numbersBeside(s, "keep 60"); strings.Join(got, " ") != "60 61" {
		t.Errorf("keep 60 is numbered %v, want old 60 and new 61", got)
	}

	// Shown lines stay shown across frames that draw the diff again, split as well as unified.
	s.Run("diff.layout", registry.Args{"layout": "split"})
	s.Quiet()
	wantKeeps(t, s, "split", [2]int{1, 9}, [2]int{11, 49}, [2]int{51, 60})
}

// Between two hunks, up from the one below: 20 lines, then what's left. GitHub's expanders are buttons with a label
// and help, as every control.
func TestDiffShowsHiddenLinesUpFromAHunk(t *testing.T) {
	s, _ := contextScene(t, "split")
	s.ClickOn("Show lines 28 to 47")
	s.Quiet()
	// new 28–47 are old 27–46.
	wantKeeps(t, s, "20 up from the second hunk", [2]int{7, 9}, [2]int{11, 13}, [2]int{27, 49}, [2]int{51, 53})
	if s.Find("Show lines 15 to 34") != nil || s.Find("Show lines 15 to 27") == nil {
		t.Error("the 13 left aren't offered whole")
	}
	if n := s.Find("Show lines 15 to 27"); n != nil && n.Role != render.RoleButton {
		t.Errorf("the expander is a %v, not a button", n.Role)
	}
}

// fold.txt's zo and zR open folds: the diff's hidden lines are its folds. zo shows 20 lines each side of the
// selected hunk, zR every hidden line of the file. A change to the file starts it over, its lines hidden again.
//
// ref: fold.txt *zo*
// quote: "Open one fold under the cursor."
// ref: fold.txt *zR*
// quote: "Open all folds."
func TestDiffKeysShowHiddenLines(t *testing.T) {
	s, r := contextScene(t, "unified")
	s.Press("]h") // the first hunk
	s.Press("zo")
	s.Quiet()
	wantKeeps(t, s, "zo on the first hunk", [2]int{1, 9}, [2]int{11, 33}, [2]int{47, 49}, [2]int{51, 53})
	s.Press("zR")
	s.Quiet()
	wantKeeps(t, s, "zR", [2]int{1, 9}, [2]int{11, 49}, [2]int{51, 60})

	_, new := contextFile()
	if err := os.WriteFile(filepath.Join(r.Dir, "lines.txt"), []byte(strings.Replace(new, "  keep 30\n", "  CHANGED 30\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	s.Until("the new change", func() bool { return strings.Contains(s.Text(), "CHANGED 30") })
	s.Quiet()
	if got := keeps(s); got[1] || got[20] || got[60] || !got[27] {
		t.Errorf("lines shown before the change are still shown after it: %v", got)
	}
}

// Shown hidden lines are highlighted as the diff's other lines are: a keyword is a run of its own.
func TestDiffHiddenLinesAreHighlighted(t *testing.T) {
	var old strings.Builder
	old.WriteString("package lines\n\n")
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&old, "func f%02d() {}\n", i)
	}
	r := gittest.New(t)
	r.Write(map[string]string{"lines.go": old.String()})
	r.Commit("Ada", "base")
	r.Write(map[string]string{"lines.go": old.String() + "func added() {}\n"})
	s := apptest.New(t, apptest.Options{WorkDir: r.Dir, Size: render.Point{X: 1200, Y: 1600}})
	s.Run("diff.open", registry.Args{"path": r.Dir})
	s.Until("the diff drawn", func() bool { return strings.Contains(s.Text(), "added") })
	s.Quiet()
	s.Press("zR")
	s.Quiet()
	runs := 0
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "f05" {
			runs++
		}
	}
	if runs == 0 {
		t.Errorf("f05's line isn't highlighted, or isn't shown:\n%s", s.Text())
	}
}
