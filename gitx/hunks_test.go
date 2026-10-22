package gitx

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/gitx/gittest"
)

// hunkFixture has a committed file of 40 lines, then in the work tree a changed line, two added lines and a deleted
// one, far enough apart to be separate hunks.
func hunkFixture(t testing.TB) (*gittest.Repo, string) {
	r := gittest.New(t)
	r.Write(map[string]string{"f.txt": gittest.Lines(40)})
	r.Commit("Ada", "base")
	work := strings.Replace(gittest.Lines(40), "line 3\n", "three\n", 1)
	work = strings.Replace(work, "line 15\n", "line 15\nnew a\nnew b\n", 1)
	work = strings.Replace(work, "line 30\n", "", 1)
	work = strings.Replace(work, "line 40\n", "forty\nforty-one\n", 1)
	r.Write(map[string]string{"f.txt": work})
	return r, work
}

// gitHunks splits git diff's patch, with context lines of context, into one patch per hunk.
func gitHunks(t testing.TB, r *gittest.Repo, context int, args ...string) []string {
	out := r.Git(append([]string{"diff", fmt.Sprintf("-U%d", context), "--no-color"}, args...)...)
	head, body, _ := strings.Cut(out, "\n@@")
	var patches []string
	for _, h := range strings.Split("@@"+body, "\n@@") {
		if !strings.HasPrefix(h, "@@") {
			h = "@@" + h
		}
		patches = append(patches, head+"\n"+strings.TrimSuffix(h, "\n")+"\n")
	}
	return patches
}

func clone(t testing.TB, r *gittest.Repo) *gittest.Repo {
	dir := t.TempDir()
	cmd := exec.Command("cp", "-a", r.Dir+"/.", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	return &gittest.Repo{T: t, Dir: dir, When: r.When}
}

// The diff finds git's hunks, with git's line numbers.
// ref: oracle git
func TestDiffLinesMatchesGit(t *testing.T) {
	r, work := hunkFixture(t)
	hs := DiffLines([]byte(gittest.Lines(40)), []byte(work))
	var got []string
	for _, h := range hs {
		got = append(got, h.Header())
	}
	var want []string
	for _, p := range gitHunks(t, r, 0, "--", "f.txt") {
		_, h, _ := strings.Cut(p, "\n@@")
		want = append(want, "@@"+strings.SplitN(h, "@@", 2)[0]+"@@")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("hunks:\n%s\nwant (git diff -U0):\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	kinds := ""
	for _, h := range hs {
		kinds += h.Kind().String() + " "
	}
	if kinds != "modified added deleted modified " {
		t.Errorf("kinds %q", kinds)
	}
}

// Staging a hunk matches git apply --cached: each hunk, staged alone, leaves the index as git apply --cached
// leaves it with git's patch for that hunk.
// ref: oracle git
func TestStageHunkMatchesGitApply(t *testing.T) {
	r, work := hunkFixture(t)
	patches := gitHunks(t, r, 0, "--", "f.txt")
	ctx := context.Background()
	for i, p := range patches {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			theirs := clone(t, r)
			cmd := exec.Command("git", "apply", "--cached", "--unidiff-zero", "-")
			cmd.Dir, cmd.Env, cmd.Stdin = theirs.Dir, gittest.Env(), strings.NewReader(p)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git apply: %v %s\n%s", err, out, p)
			}
			ours := clone(t, r)
			repo := Repo{Dir: ours.Dir}
			hs, err := repo.UnstagedHunks(ctx, "f.txt", []byte(work))
			if err != nil {
				t.Fatal(err)
			}
			if len(hs) != len(patches) {
				t.Fatalf("%d hunks, git has %d", len(hs), len(patches))
			}
			if err := repo.StageHunk(ctx, "f.txt", []byte(work), hs[i]); err != nil {
				t.Fatal(err)
			}
			if got, want := ours.Git("show", ":f.txt"), theirs.Git("show", ":f.txt"); got != want {
				t.Errorf("index after staging hunk %d:\n%s\nwant:\n%s", i, got, want)
			}
			if got, want := ours.Git("diff", "--cached", "--stat"), theirs.Git("diff", "--cached", "--stat"); got != want {
				t.Errorf("staged stat %q, want %q", got, want)
			}
			b, _ := os.ReadFile(filepath.Join(ours.Dir, "f.txt"))
			if string(b) != work {
				t.Error("staging a hunk changed the work tree")
			}
		})
	}
}

// Unstaging a staged hunk puts the index back as git apply --cached --reverse does. git's patch has context here:
// reversed, a zero-context deletion can't be placed reliably (git apply's --unidiff-zero says so).
// ref: oracle git
func TestUnstageHunkMatchesGitApplyReverse(t *testing.T) {
	r, _ := hunkFixture(t)
	r.Git("add", "f.txt")
	patches := gitHunks(t, r, 3, "--cached", "--", "f.txt")
	ctx := context.Background()
	for i, p := range patches {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			theirs := clone(t, r)
			cmd := exec.Command("git", "apply", "--cached", "--reverse", "-")
			cmd.Dir, cmd.Env, cmd.Stdin = theirs.Dir, gittest.Env(), strings.NewReader(p)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git apply: %v %s", err, out)
			}
			ours := clone(t, r)
			repo := Repo{Dir: ours.Dir}
			hs, err := repo.StagedHunks(ctx, "f.txt")
			if err != nil {
				t.Fatal(err)
			}
			if err := repo.UnstageHunk(ctx, "f.txt", hs[i]); err != nil {
				t.Fatal(err)
			}
			if got, want := ours.Git("show", ":f.txt"), theirs.Git("show", ":f.txt"); got != want {
				t.Errorf("index after unstaging hunk %d:\n%s\nwant:\n%s", i, got, want)
			}
		})
	}
}

// A new file's one hunk stages it whole; staging keeps the file's mode.
// ref: oracle git
func TestStageHunkNewAndExecutable(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"run.sh": "echo 1\n"})
	os.Chmod(filepath.Join(r.Dir, "run.sh"), 0o755)
	r.Commit("Ada", "base")
	r.Write(map[string]string{"run.sh": "echo 2\n", "new.txt": "a\nb\n"})
	os.Chmod(filepath.Join(r.Dir, "run.sh"), 0o755)
	repo, ctx := Repo{Dir: r.Dir}, context.Background()
	for _, f := range []struct{ path, body string }{{"run.sh", "echo 2\n"}, {"new.txt", "a\nb\n"}} {
		hs, err := repo.UnstagedHunks(ctx, f.path, []byte(f.body))
		if err != nil || len(hs) != 1 {
			t.Fatalf("%s: %v %v", f.path, hs, err)
		}
		if err := repo.StageHunk(ctx, f.path, []byte(f.body), hs[0]); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.Git("diff", "--cached", "--name-status"); got != "A\tnew.txt\nM\trun.sh\n" {
		t.Errorf("staged: %q", got)
	}
	if got := r.Git("ls-files", "-s", "run.sh"); !strings.HasPrefix(got, "100755") {
		t.Errorf("run.sh's mode: %q", got)
	}
}

// Applying every hunk of a diff, one at a time from the last, turns the old text into the new, for random edits.
func TestDiffLinesProperty(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	words := []string{"a\n", "b\n", "c\n", "d\n", "e\n"}
	gen := func() []string {
		var out []string
		for range rnd.IntN(30) {
			out = append(out, words[rnd.IntN(len(words))])
		}
		return out
	}
	for i := range 2000 {
		a, b := gen(), gen()
		hs := diffLines(a, b)
		got := a
		for j := len(hs) - 1; j >= 0; j-- {
			got = applyHunk(got, hs[j])
		}
		if strings.Join(got, "") != strings.Join(b, "") {
			t.Fatalf("case %d: %q -> %q gave %q via %v", i, a, b, got, hs)
		}
		back := b
		for j := len(hs) - 1; j >= 0; j-- {
			back = revertHunk(back, hs[j])
		}
		if strings.Join(back, "") != strings.Join(a, "") {
			t.Fatalf("case %d: reverting gave %q, want %q", i, back, a)
		}
	}
}

// FileAt reads a file as a revision has it, as git show rev:path does.
// ref: oracle git
func TestFileAt(t *testing.T) {
	r, _ := hunkFixture(t)
	repo := Repo{Dir: r.Dir}
	base := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	got, ok := repo.FileAt(context.Background(), base, "f.txt")
	if want := r.Git("show", base+":f.txt"); !ok || string(got) != want {
		t.Errorf("FileAt = %q, %v; want git show's", got, ok)
	}
	if _, ok := repo.FileAt(context.Background(), base, "nope.txt"); ok {
		t.Error("a file the revision hasn't got was found")
	}
}
