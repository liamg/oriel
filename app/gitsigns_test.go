package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/vim"
)

// signsScene is f.txt committed by you with 30 lines, then in the work tree line 3 changed, two lines added after
// line 10 and line 20 deleted, open in the editor.
func signsScene(t *testing.T) (*errScene, *gittest.Repo) {
	r := gittest.New(t)
	r.Git("config", "user.email", "ada@example.com")
	r.Write(map[string]string{"f.txt": gittest.Lines(30)})
	r.Commit("Ada", "thirty lines")
	work := strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1)
	work = strings.Replace(work, "line 10\n", "line 10\nnew a\nnew b\n", 1)
	work = strings.Replace(work, "line 20\n", "", 1)
	r.Write(map[string]string{"f.txt": work})
	s := newScene(t, "", r.Dir, mockupSize)
	s.sh.git.wait()
	s.signs().now = func() time.Time { return r.When.Add(3 * 24 * time.Hour) }
	s.run("editor.open", registry.Args{"path": filepath.Join(r.Dir, "f.txt")})
	s.settle()
	return s, r
}

func (s *errScene) signs() *gitSigns {
	for _, e := range s.plugins {
		if g, ok := e.(*gitSigns); ok {
			return g
		}
	}
	return nil
}

// settle frames until the gutter's and blame's work off the loop is done.
func (s *errScene) settle() {
	for range 3 {
		s.frame()
		s.sh.git.wait()
		s.signs().wait()
		s.sh.runTodo()
	}
	s.frame()
}

// signsAt are the gutter signs the editor draws on each line, "line:style".
func (s *errScene) signsAt() string {
	x := s.editor()
	p := x.current()
	var out []string
	ds := x.decorations(p, 0, p.Buf().Lines(), nil)
	for line := range p.Buf().Lines() {
		for _, d := range ds[line] {
			if d.Kind == registry.Sign && strings.HasPrefix(d.Style, "git.") {
				out = append(out, strings.TrimPrefix(d.Style, "git.")+"@"+fmt.Sprint(line+1))
			}
		}
	}
	return strings.Join(out, " ")
}

func (s *errScene) cursorTo(line int) {
	s.editor().e.SetCursor(vim.Pos{Line: line - 1})
	s.settle()
}

// FG-2: gutter hunks: added, modified and deleted lines are signed against the index, and edits not yet saved count.
// ref: oracle git
func TestGutterHunks(t *testing.T) {
	s, _ := signsScene(t)
	if got, want := s.signsAt(), "modified@3 added@11 added@12 deleted@21"; got != want {
		t.Errorf("signs %q, want %q", got, want)
	}
	s.keys("ggOtop<Esc>")
	s.settle()
	if got, want := s.signsAt(), "added@1 modified@4 added@12 added@13 deleted@22"; got != want {
		t.Errorf("after an unsaved line at the top, signs %q, want %q", got, want)
	}
}

// FG-2: a hunk's bar fills each of its lines, so a run of changed lines is one unbroken bar.
func TestGutterBarsJoin(t *testing.T) {
	s, _ := signsScene(t)
	s.frame()
	added := s.ui.Theme().C(tGitAdded)
	var bars []render.Rect
	for _, op := range s.list.Ops {
		if op.Kind == render.OpRect && op.Style.Fill == added && op.Rect.Dx() < 8 {
			bars = append(bars, op.Rect)
		}
	}
	if len(bars) != 2 {
		t.Fatalf("%d added bars, want 2 (lines 11 and 12): %v", len(bars), bars)
	}
	if lh := s.editor().pane.lineH; bars[0].Dy() != lh || bars[1].Min.Y != bars[0].Max.Y {
		t.Errorf("the added bars %v don't join up (a line is %v high)", bars, lh)
	}
}

// FG-2: ␣hs stages the hunk under the cursor, from the buffer, as git apply --cached would; its sign goes.
// ref: oracle git
func TestStageHunkFromTheEditor(t *testing.T) {
	s, r := signsScene(t)
	if k, _ := s.reg.Lookup([]string{"n"}, []string{" ", "h", "s"}); k == nil || k.Command != "git.hunk.stage" {
		t.Fatalf("␣hs is %+v", k)
	}
	s.cursorTo(11)
	s.run("git.hunk.stage", nil)
	s.settle()
	want := strings.Replace(gittest.Lines(30), "line 10\n", "line 10\nnew a\nnew b\n", 1)
	if got := r.Git("show", ":f.txt"); got != want {
		t.Errorf("the index after ␣hs:\n%s", got)
	}
	if got := s.signsAt(); got != "modified@3 deleted@21" {
		t.Errorf("signs after staging %q", got)
	}
	s.run("git.hunk.unstage", nil)
	s.settle()
	if got := r.Git("show", ":f.txt"); got != gittest.Lines(30) {
		t.Errorf("␣hu didn't unstage it:\n%s", got)
	}
}

// ␣hr puts a hunk's lines back as the index has them, in the buffer, as one undoable change.
func TestResetHunk(t *testing.T) {
	s, _ := signsScene(t)
	s.cursorTo(21)
	s.run("git.hunk.reset", nil)
	s.settle()
	if got := s.line(21); got != "line 20" {
		t.Errorf("line 22 after resetting the deletion is %q", got)
	}
	s.cursorTo(3)
	s.run("git.hunk.reset", nil)
	s.settle()
	if got := s.line(2); got != "line 3" {
		t.Errorf("line 3 after resetting it is %q", got)
	}
	if got := s.signsAt(); got != "added@11 added@12" {
		t.Errorf("signs after two resets %q", got)
	}
	s.keys("u")
	if got := s.line(2); got != "three" {
		t.Errorf("u didn't undo the reset: %q", got)
	}
}

// ]c and [c move between hunks.
func TestHunkMotions(t *testing.T) {
	s, _ := signsScene(t)
	s.cursorTo(1)
	for _, want := range []int{3, 11, 21, 21} {
		s.run("git.hunk.next", nil)
		if got := s.editor().e.Cursor().Line + 1; got != want {
			t.Errorf("]c went to %d, want %d", got, want)
		}
	}
	s.run("git.hunk.prev", nil)
	if got := s.editor().e.Cursor().Line + 1; got != 11 {
		t.Errorf("[c went to %d, want 11", got)
	}
	for _, k := range []string{"]c", "[c"} {
		if m, _ := s.reg.Lookup([]string{"n"}, []string{k[:1], k[1:]}); m == nil {
			t.Errorf("%s isn't mapped", k)
		}
	}
}

// ␣hp shows what a hunk replaced, between the lines, until it's pressed again.
func TestPreviewHunk(t *testing.T) {
	s, _ := signsScene(t)
	s.cursorTo(3)
	s.run("git.hunk.preview", nil)
	s.settle()
	if !strings.Contains(s.frameText(), "- line 3") {
		t.Fatalf("no preview of the old line:\n%s", s.frameText())
	}
	s.run("git.hunk.preview", nil)
	s.settle()
	if strings.Contains(s.frameText(), "- line 3") {
		t.Error("a second ␣hp didn't close the preview")
	}
}

// FG-2: inline blame by author kind: the cursor's line says who wrote it (you, an agent, someone else) and when, or
// that it isn't committed yet.
// ref: oracle git
func TestInlineBlame(t *testing.T) {
	s, r := signsScene(t)
	s.cursorTo(1)
	if text := s.frameText(); !strings.Contains(text, "You · 3d ago · thirty lines") {
		t.Errorf("line 1's blame:\n%s", text)
	}
	s.cursorTo(3)
	if text := s.frameText(); !strings.Contains(text, "Not committed yet") {
		t.Errorf("line 3's blame:\n%s", text)
	}
	b, _ := os.ReadFile(filepath.Join(r.Dir, "f.txt"))
	r.Write(map[string]string{"f.txt": string(b)})
	r.Commit("Claude", "agent's rounding")
	s.sh.git.wait()
	s.run("git.refresh", nil)
	s.settle()
	s.cursorTo(3)
	s.settle()
	if text := s.frameText(); !strings.Contains(text, "Claude (agent) · ") {
		t.Errorf("an agent's line:\n%s", text)
	}
	s.run("git.blame.toggle", nil)
	s.settle()
	if text := s.frameText(); strings.Contains(text, "Claude (agent)") {
		t.Error("␣hb didn't hide the blame")
	}
}
