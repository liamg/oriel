package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/agent"
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

// A checkout the agent reads without a worktree of its own (a plan session's, until Start work) is in the sandbox at
// its own path, read-only: the agent reads it and can't change it.
func TestBindsReadTheCheckouts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	gitT(t, repo, "init", "-q")
	root := t.TempDir()
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	if err := SaveManifest(root, Manifest{Reads: []string{repo}}); err != nil {
		t.Fatal(err)
	}
	binds, err := Binds(root, root, agent.SandboxNeeds{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range binds {
		if b.Host == repo {
			if b.Agent != repo || !b.ReadOnly {
				t.Errorf("the checkout is bound %+v", b)
			}
			return
		}
	}
	t.Errorf("the checkout isn't in the sandbox: %+v", binds)
}

// A workspace's agent keeps its memory in its repository's Claude Code memory directory on the host, read-write.
func TestBindsGiveTheRepositorysMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	mem := MemoryDir("/src/oriel")
	if err := SaveManifest(root, Manifest{Memory: []ProjectMemory{{Name: "oriel", Dir: mem}}}); err != nil {
		t.Fatal(err)
	}
	binds, err := Binds(root, root, agent.SandboxNeeds{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(binds, Bind{Host: mem, Agent: mem}) {
		t.Errorf("no read-write bind of %s: %+v", mem, binds)
	}
	if fi, err := os.Stat(mem); err != nil || !fi.IsDir() {
		t.Errorf("the memory directory wasn't made: %v", err)
	}
}

// A workspace with no projects keeps Claude Code's own memory, under its transcripts.
func TestBindsWithNoProjectsGiveNoMemory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := Create(root, ModeSandbox); err != nil {
		t.Fatal(err)
	}
	binds, err := Binds(root, root, agent.SandboxNeeds{})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range binds {
		if filepath.Base(b.Host) == "memory" {
			t.Errorf("a memory bind: %+v", b)
		}
	}
}
