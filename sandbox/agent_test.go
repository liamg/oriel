package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestGitBindsGuardHostGit(t *testing.T) {
	repo, wt := t.TempDir(), filepath.Join(t.TempDir(), "wt")
	gitT(t, repo, "init", "-q")
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	gitT(t, repo, "worktree", "add", "-q", wt)
	common := filepath.Join(repo, ".git")
	for _, d := range []string{"hooks", "info"} {
		if err := os.RemoveAll(filepath.Join(common, d)); err != nil {
			t.Fatal(err)
		}
	}
	m, err := GitBinds(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, mt := range m {
		if mt.Host != mt.Agent {
			t.Errorf("%s mounted at %s", mt.Host, mt.Agent)
		}
		got[mt.Agent] = mt.ReadOnly
	}
	admin, err := WorktreeAdmin(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"config", "worktrees", "HEAD", "index", "hooks", "modules", "info", "remotes", "branches", "config.worktree"} {
		p = filepath.Join(common, p)
		if ro, ok := got[p]; !ok || !ro {
			t.Errorf("%s: mounted %t, read-only %t", p, ok, ro)
		}
		if _, err := os.Stat(p); err != nil {
			t.Errorf("no placeholder for %s: %v", p, err)
		}
	}
	for p, want := range map[string]bool{common: false, admin: false, filepath.Join(admin, "gitdir"): true, filepath.Join(admin, "commondir"): true} {
		if ro, ok := got[p]; !ok || ro != want {
			t.Errorf("%s: mounted %t, read-only %t, want %t", p, ok, ro, want)
		}
	}
	// The placeholders leave both checkouts working.
	gitT(t, wt, "commit", "-q", "--allow-empty", "-m", "y")
	gitT(t, repo, "status", "--short")
}
