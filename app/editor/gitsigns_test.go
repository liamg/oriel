package editor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// signsAt are the gutter signs the editor draws on each line, "line:style".
func signsAt(s *apptest.Scene) string {
	x := editorOf(s)
	p := x.Current()
	var out []string
	ds := x.Decorations(p, 0, p.Buf().Lines())
	for line := range p.Buf().Lines() {
		for _, d := range ds[line] {
			if d.Kind == uireg.Sign && strings.HasPrefix(d.Style, "git.") {
				out = append(out, strings.TrimPrefix(d.Style, "git.")+"@"+fmt.Sprint(line+1))
			}
		}
	}
	return strings.Join(out, " ")
}

// Gutter hunks: added, modified and deleted lines are signed against the index, and edits not yet saved count.
// ref: oracle git
func TestGutterHunks(t *testing.T) {
	s, _ := signsScene(t)
	if got, want := signsAt(s), "modified@3 added@11 added@12 deleted@21"; got != want {
		t.Errorf("signs %q, want %q", got, want)
	}
	keys(s, "ggOtop<Esc>")
	settle(s)
	if got, want := signsAt(s), "added@1 modified@4 added@12 added@13 deleted@22"; got != want {
		t.Errorf("after an unsaved line at the top, signs %q, want %q", got, want)
	}
}

// The gutter's hunks are diffed off the loop: an edit's frames show the hunks from before it until the
// diff lands, never a diff made in the frame.
func TestGutterHunksDiffOffTheLoop(t *testing.T) {
	s, _ := signsScene(t)
	before := signsAt(s)
	keys(s, "ggOtop<Esc>")
	if got := signsAt(s); got != before {
		t.Errorf("the frame after an edit signs %q, want the last hunks %q until the diff lands", got, before)
	}
	settle(s)
	if got, want := signsAt(s), "added@1 modified@4 added@12 added@13 deleted@22"; got != want {
		t.Errorf("once the diff landed, signs %q, want %q", got, want)
	}
}

// A hunk's bar fills each of its lines, so a run of changed lines is one unbroken bar.
func TestGutterBarsJoin(t *testing.T) {
	s, _ := signsScene(t)
	s.Frame()
	added := s.UI().Theme().C(kit.TGitAdded)
	var bars []render.Rect
	for _, op := range s.List().Ops {
		if op.Kind == render.OpRect && op.Style.Fill == added && op.Rect.Dx() < 8 {
			bars = append(bars, op.Rect)
		}
	}
	if len(bars) != 2 {
		t.Fatalf("%d added bars, want 2 (lines 11 and 12): %v", len(bars), bars)
	}
	if lh := editorOf(s).Metrics().LineH; bars[0].Dy() != lh || bars[1].Min.Y != bars[0].Max.Y {
		t.Errorf("the added bars %v don't join up (a line is %v high)", bars, lh)
	}
}

// ␣hs stages the hunk under the cursor, from the buffer, as git apply --cached would; its sign goes.
// ref: oracle git
func TestStageHunkFromTheEditor(t *testing.T) {
	s, r := signsScene(t)
	if k, _ := s.Registry().Lookup([]string{"n"}, []string{" ", "h", "s"}); k == nil || k.Command != "git.hunk.stage" {
		t.Fatalf("␣hs is %+v", k)
	}
	cursorTo(s, 11)
	s.Run("git.hunk.stage", nil)
	settle(s)
	want := strings.Replace(gittest.Lines(30), "line 10\n", "line 10\nnew a\nnew b\n", 1)
	if got := r.Git("show", ":f.txt"); got != want {
		t.Errorf("the index after ␣hs:\n%s", got)
	}
	if got := signsAt(s); got != "modified@3 deleted@21" {
		t.Errorf("signs after staging %q", got)
	}
	s.Run("git.hunk.unstage", nil)
	settle(s)
	if got := r.Git("show", ":f.txt"); got != gittest.Lines(30) {
		t.Errorf("␣hu didn't unstage it:\n%s", got)
	}
}

// ␣hr puts a hunk's lines back as the index has them, in the buffer, as one undoable change.
func TestResetHunk(t *testing.T) {
	s, _ := signsScene(t)
	cursorTo(s, 21)
	s.Run("git.hunk.reset", nil)
	settle(s)
	if got := s.Line(21); got != "line 20" {
		t.Errorf("line 22 after resetting the deletion is %q", got)
	}
	cursorTo(s, 3)
	s.Run("git.hunk.reset", nil)
	settle(s)
	if got := s.Line(2); got != "line 3" {
		t.Errorf("line 3 after resetting it is %q", got)
	}
	if got := signsAt(s); got != "added@11 added@12" {
		t.Errorf("signs after two resets %q", got)
	}
	keys(s, "u")
	if got := s.Line(2); got != "three" {
		t.Errorf("u didn't undo the reset: %q", got)
	}
}

// ]c and [c move between hunks.
func TestHunkMotions(t *testing.T) {
	s, _ := signsScene(t)
	cursorTo(s, 1)
	for _, want := range []int{3, 11, 21, 21} {
		s.Run("git.hunk.next", nil)
		if got := engine(s).Cursor().Line + 1; got != want {
			t.Errorf("]c went to %d, want %d", got, want)
		}
	}
	s.Run("git.hunk.prev", nil)
	if got := engine(s).Cursor().Line + 1; got != 11 {
		t.Errorf("[c went to %d, want 11", got)
	}
	for _, k := range []string{"]c", "[c"} {
		if m, _ := s.Registry().Lookup([]string{"n"}, []string{k[:1], k[1:]}); m == nil {
			t.Errorf("%s isn't mapped", k)
		}
	}
}

// ␣hp shows what a hunk replaced, between the lines, until it's pressed again.
func TestPreviewHunk(t *testing.T) {
	s, _ := signsScene(t)
	cursorTo(s, 3)
	s.Run("git.hunk.preview", nil)
	settle(s)
	if !strings.Contains(s.Text(), "- line 3") {
		t.Fatalf("no preview of the old line:\n%s", s.Text())
	}
	s.Run("git.hunk.preview", nil)
	settle(s)
	if strings.Contains(s.Text(), "- line 3") {
		t.Error("a second ␣hp didn't close the preview")
	}
}

// Inline blame by author kind: the cursor's line says who wrote it (you, an agent, someone else) and when, or
// that it isn't committed yet.
// ref: oracle git
func TestInlineBlame(t *testing.T) {
	s, r := signsScene(t)
	cursorTo(s, 1)
	if text := s.Text(); !strings.Contains(text, "You · 3d ago · thirty lines") {
		t.Errorf("line 1's blame:\n%s", text)
	}
	cursorTo(s, 3)
	if text := s.Text(); !strings.Contains(text, "Not committed yet") {
		t.Errorf("line 3's blame:\n%s", text)
	}
	b, _ := os.ReadFile(filepath.Join(r.Dir, "f.txt"))
	r.Write(map[string]string{"f.txt": string(b)})
	r.Commit("Claude", "agent's rounding")
	s.SettleGit()
	s.Run("git.refresh", nil)
	settle(s)
	cursorTo(s, 3)
	settle(s)
	if text := s.Text(); !strings.Contains(text, "Claude (agent) · ") {
		t.Errorf("an agent's line:\n%s", text)
	}
	s.Run("git.blame.toggle", nil)
	settle(s)
	if text := s.Text(); strings.Contains(text, "Claude (agent)") {
		t.Error("␣hb didn't hide the blame")
	}
}
