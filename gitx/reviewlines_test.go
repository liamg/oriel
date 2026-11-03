package gitx

import (
	"context"
	"slices"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

func sortedLines(l map[int]bool) []int {
	var out []int
	for n := range l {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// GitHub patches are unified diff: a patch's hunks are unified diff, "@@ -old,n +new,m @@" then context, - and + lines.
func TestPatchLines(t *testing.T) {
	l := PatchLines("@@ -1,2 +1,3 @@\n a\n-b\n+c\n+d\n\\ No newline at end of file\n@@ -20 +21,2 @@\n x\n+y")
	for side, want := range map[string][]int{"RIGHT": {1, 2, 3, 21, 22}, "LEFT": {1, 2, 20}} {
		if got := sortedLines(l[side]); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", side, got, want)
		}
	}
}

// A review comment sits on a line of the PR's diff: a changed line, or one of the three lines of context git
// shows either side of a change. Files the change doesn't touch have none; a deleted file's are on its old side.
// ref: oracle git
func TestReviewLines(t *testing.T) {
	r := gittest.New(t)
	long := ""
	for i := 1; i <= 20; i++ {
		long += "line\n"
	}
	r.Write(map[string]string{"long.go": long, "same.go": "package same\n", "gone.go": "a\nb\n"})
	r.Commit("Ada", "base")
	r.Git("checkout", "-q", "-b", "feature")
	r.Write(map[string]string{"long.go": long[:5*5] + "changed\n" + long[6*5:], "new.go": "x\ny\n"})
	r.Git("rm", "-q", "gone.go")
	r.Commit("Claude", "change")
	got, err := Repo{Dir: r.Dir}.ReviewLines(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["same.go"]; ok {
		t.Error("same.go isn't in the change")
	}
	for _, c := range []struct {
		file, side string
		want       []int
	}{
		{"long.go", "RIGHT", []int{3, 4, 5, 6, 7, 8, 9}},
		{"long.go", "LEFT", []int{3, 4, 5, 6, 7, 8, 9}},
		{"new.go", "RIGHT", []int{1, 2}},
		{"gone.go", "LEFT", []int{1, 2}},
	} {
		if l := sortedLines(got[c.file][c.side]); !slices.Equal(l, c.want) {
			t.Errorf("%s %s: %v, want %v", c.file, c.side, l, c.want)
		}
	}
}
