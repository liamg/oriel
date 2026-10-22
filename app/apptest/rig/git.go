package rig

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Git runs git in dir as x, failing the test if it fails, and answers what it said.
func Git(t testing.TB, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// WriteFiles writes files, by their paths in dir.
func WriteFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		WriteFile(t, filepath.Join(dir, name), body)
	}
}

// ShipRepo is a fixture checkout whose origin is https://github.com/acme/<name>.git, answered by a bare repo on disk
// (git's url.insteadOf), with main pushed; it returns the checkout and a second clone that moves main on.
func ShipRepo(t *testing.T, name string, files map[string]string) (checkout, upstream string) {
	t.Helper()
	root := t.TempDir()
	origins := filepath.Join(root, "origins", "acme")
	checkout, upstream = filepath.Join(root, name), filepath.Join(root, "upstream-"+name)
	Git(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(origins, name+".git"))
	Git(t, root, "init", "-q", "-b", "main", checkout)
	WriteFiles(t, checkout, files)
	Git(t, checkout, "add", "-A")
	Git(t, checkout, "commit", "-q", "-m", "first")
	Git(t, checkout, "remote", "add", "origin", "https://github.com/acme/"+name+".git")
	Git(t, checkout, "config", "url."+origins+"/.insteadOf", "https://github.com/acme/")
	Git(t, checkout, "push", "-q", "origin", "main")
	Git(t, checkout, "fetch", "-q", "origin")
	Git(t, checkout, "remote", "set-head", "origin", "main")
	Git(t, root, "clone", "-q", filepath.Join(origins, name+".git"), upstream)
	return checkout, upstream
}

// NewRepo is a git repo at dir with one empty commit on main.
func NewRepo(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main", dir}, {"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}
