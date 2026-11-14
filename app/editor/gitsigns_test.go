package editor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
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

// The gutter signs a branch's whole change, against where it left its base: committed, staged and unstaged alike,
// and a renamed file against its old name (git.gutter.base); "head" signs what isn't committed, "index" what isn't
// staged.
// ref: oracle git (git diff -U0 -M $(git merge-base HEAD main); git diff -U0 HEAD; git diff -U0)
func TestGutterSignsTheBranch(t *testing.T) {
	s, _ := apptest.BranchSignsScene(t, "f.txt")
	if got, want := signsAt(s), "modified@3 added@11 added@12 deleted@21"; got != want {
		t.Errorf("against the base, signs %q, want %q", got, want)
	}
	for _, c := range []struct{ base, want string }{
		{"head", "added@11 added@12 deleted@21"},
		{"index", "deleted@21"},
		{"branch", "modified@3 added@11 added@12 deleted@21"},
	} {
		if err := s.Registry().Options().Set("git.gutter.base", registry.LayerUser, registry.At{}, c.base); err != nil {
			t.Fatal(err)
		}
		settle(s)
		if got := signsAt(s); got != c.want {
			t.Errorf("against %s, signs %q, want %q", c.base, got, c.want)
		}
	}
	renamed, _ := apptest.BranchSignsScene(t, "new.txt")
	if got, want := signsAt(renamed), "modified@5"; got != want {
		t.Errorf("a file renamed on the branch signs %q, want %q", got, want)
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
	if got := signsAt(s); got != "modified@3 added@11 added@12 deleted@21" {
		t.Errorf("against the base, signs after staging %q: a staged hunk is still the branch's", got)
	}
	if err := s.Registry().Options().Set("git.gutter.base", registry.LayerUser, registry.At{}, "index"); err != nil {
		t.Fatal(err)
	}
	settle(s)
	if got := signsAt(s); got != "modified@3 deleted@21" {
		t.Errorf("against the index, signs after staging %q", got)
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

// Typing doesn't diff the file for the gutter at every key: its signs wait for the buffer to be still a moment, then
// are right.
func TestGutterWaitsForTypingToStop(t *testing.T) {
	s, _ := signsScene(t)
	g := signsOf(s)
	before := g.Diffs()
	keys(s, "ggO")
	for _, k := range "typing" { // as quick as a person types: well inside the wait between keys
		keys(s, string(k))
		s.Frame()
		s.Advance(60 * time.Millisecond)
		s.RunQueued()
		s.Frame()
	}
	keys(s, "<Esc>")
	s.Frame()
	if n := g.Diffs() - before; n > 0 {
		t.Errorf("typing 6 letters began %d diffs", n)
	}
	settle(s)
	if got, want := signsAt(s), "added@1 modified@4 added@12 added@13 deleted@22"; got != want {
		t.Errorf("once still, signs %q, want %q", got, want)
	}
}

// Another file's change leaves the open file's gutter base as it was read: only the index's copy of a file whose
// status changed is read again.
func TestAnotherFilesChangeKeepsTheBase(t *testing.T) {
	s, r := signsScene(t)
	g := signsOf(s)
	before := g.BaseReads()
	if before == 0 {
		t.Fatal("no base read for the open file")
	}
	r.Write(map[string]string{"other.txt": "another file\n"})
	s.Until("the other file's status", func() bool {
		s.Frame()
		st := kit.GitOf(s.Registry()).Tree("").State()
		if st == nil {
			return false
		}
		_, ok := st.File("other.txt")
		return ok
	})
	settle(s)
	if n := g.BaseReads() - before; n > 0 {
		t.Errorf("a new file read the open file's base %d more times", n)
	}
	if got, want := signsAt(s), "modified@3 added@11 added@12 deleted@21"; got != want {
		t.Errorf("signs %q, want %q", got, want)
	}
}

// A project's file in a workspace signs against the project's repo, though the workspace's own repo ignores it.
// ref: oracle git (git -C repos/p diff -U0 HEAD)
func TestGutterSignsAProjectInAWorkspace(t *testing.T) {
	ws := gittest.New(t)
	ws.Write(map[string]string{".git/info/exclude": "repos/\n", "README.md": "workspace\n"})
	ws.Commit("Ada", "workspace")
	p := &gittest.Repo{T: t, Dir: filepath.Join(ws.Dir, "repos", "p"), When: ws.When}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p.Git("init", "-q", "-b", "main")
	p.Write(map[string]string{"f.txt": gittest.Lines(30)})
	p.Commit("Ada", "thirty lines")
	p.Write(map[string]string{"f.txt": strings.Replace(gittest.Lines(30), "line 3\n", "three\n", 1)})

	s := apptest.New(t, apptest.Options{WorkDir: ws.Dir})
	se := core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Dir: ws.Dir,
		Projects: []core.Project{{Name: "p", Dir: p.Dir, Primary: true}}}
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	s.WaitGit()
	s.Run("editor.open", registry.Args{"path": filepath.Join(p.Dir, "f.txt")})
	s.Until("the project's signs", func() bool { settle(s); return signsAt(s) != "" })
	if got, want := signsAt(s), "modified@3"; got != want {
		t.Errorf("signs %q, want %q", got, want)
	}
}

// The gutter follows the Explorer's scope by default (git.gutter.base "scope"): with Uncommitted showing it signs
// only what isn't committed, against the last commit; with Changed, the branch's whole change against its base.
// ref: oracle git (git diff -U0 HEAD; git diff -U0 $(git merge-base HEAD main))
func TestGutterFollowsTheExplorersScope(t *testing.T) {
	s, _ := apptest.BranchSignsScene(t, "f.txt")
	for _, c := range []struct{ keys, scope, want string }{
		{"<M-2>", "Uncommitted", "added@11 added@12 deleted@21"},
		{"<M-1>", "Changed", "modified@3 added@11 added@12 deleted@21"},
	} {
		s.Press(c.keys)
		settle(s)
		if got := signsAt(s); got != c.want {
			t.Errorf("with %s showing, signs %q, want %q", c.scope, got, c.want)
		}
	}
}
