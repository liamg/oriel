package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/sandbox"
)

func TestMountsShareAttachments(t *testing.T) {
	if !sandbox.SocketsMount() {
		t.Skip("needs the session socket on the host")
	}
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	run := sandbox.RunDir(root)
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run, "oriel.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Mounts(root, root, sandbox.Identity{Home: "/home/u", UID: 501}, "/bin/claude", "/bin/oriel", "")
	if err != nil {
		t.Fatal(err)
	}
	state, _ := sandbox.StateDir(root)
	want := filepath.Join(state, "attachments")
	for _, mt := range m {
		if mt.Target == want {
			if mt.Source != want || !mt.ReadOnly {
				t.Fatalf("attachments mounted as %+v", mt)
			}
			return
		}
	}
	t.Fatalf("no mount for %s in %+v", want, m)
}

func TestMountsShareGoCache(t *testing.T) {
	if !sandbox.SocketsMount() {
		t.Skip("needs the session socket on the host")
	}
	t.Setenv("HOME", t.TempDir())
	id := sandbox.Identity{Home: "/home/u", UID: 501}
	var sources []string
	for range 2 {
		root := t.TempDir()
		if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(sandbox.RunDir(root), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sandbox.SocketPath(root), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		m, err := Mounts(root, root, id, "/bin/claude", "/bin/oriel", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, mt := range m {
			if mt.Target == GoCacheDir {
				if mt.ReadOnly {
					t.Fatalf("go cache mounted read-only: %+v", mt)
				}
				sources = append(sources, mt.Source)
			}
		}
	}
	if len(sources) != 2 || sources[0] != sources[1] {
		t.Fatalf("go cache sources %v, want one shared dir", sources)
	}
	if fi, err := os.Stat(sources[0]); err != nil || !fi.IsDir() {
		t.Fatalf("go cache dir: %v", err)
	}
	env := agentEnv(t.TempDir(), id, sandbox.Config{}, Options{Environ: []string{"GOMODCACHE=/host/mod"}})
	if env["GOMODCACHE"] != GoCacheDir+"/mod" || env["GOCACHE"] != GoCacheDir+"/build" {
		t.Fatalf("GOMODCACHE=%q GOCACHE=%q", env["GOMODCACHE"], env["GOCACHE"])
	}
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

func TestGitMountsGuardHostGit(t *testing.T) {
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
	m, err := gitMounts(repo, wt)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, mt := range m {
		if mt.Source != mt.Target {
			t.Errorf("%s mounted at %s", mt.Source, mt.Target)
		}
		got[mt.Target] = mt.ReadOnly
	}
	admin, err := sandbox.WorktreeAdmin(repo, wt)
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
