package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeWorktree lays out what git leaves for a linked worktree: a clone with a
// real .git directory, and a folder whose .git file points into it.
func fakeWorktree(t *testing.T) (clone, wt string) {
	t.Helper()
	clone = filepath.Join(t.TempDir(), "coast")
	admin := filepath.Join(clone, ".git", "worktrees", "coast-refactor")
	if err := os.MkdirAll(admin, 0o755); err != nil {
		t.Fatal(err)
	}
	wt = filepath.Join(t.TempDir(), "coast-refactor")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return clone, wt
}

// A record saved with a worktree as its repo names the clone when loaded,
// and keeps it: the guard finds the session's git data only under the
// clone's own .git.
func TestLoadRepairsAWorktreeRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clone, wt := fakeWorktree(t)
	root := filepath.Join(t.TempDir(), "pr-1")
	if err := Save(Record{Root: root, Mode: "sandbox", Repo: wt}); err != nil {
		t.Fatal(err)
	}

	r, ok := Load(root)
	if !ok || r.Repo != clone {
		t.Fatalf("repo = %q, want the clone %q", r.Repo, clone)
	}
	b, _ := os.ReadFile(recordPath(root))
	if want := `"repo": "` + clone + `"`; !strings.Contains(string(b), want) {
		t.Errorf("record on disk = %s, want the repair saved", b)
	}
}

// A clone, or a path that is gone, is left as it is.
func TestLoadKeepsARealRepo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	clone, _ := fakeWorktree(t)
	for _, repo := range []string{clone, filepath.Join(t.TempDir(), "gone")} {
		root := filepath.Join(t.TempDir(), "s")
		if err := Save(Record{Root: root, Mode: "sandbox", Repo: repo}); err != nil {
			t.Fatal(err)
		}
		if r, _ := Load(root); r.Repo != repo {
			t.Errorf("repo %q came back as %q", repo, r.Repo)
		}
	}
}
