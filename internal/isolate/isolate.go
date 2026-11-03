// Package isolate keeps a test from reaching the tests after it: every harness that starts an app, window or host
// calls Test first.
package isolate

import (
	"cmp"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/liamg/oriel/internal/leaktest"
)

var started sync.Map // test name → true, while that test runs

var userHome = os.Getenv("HOME") // the HOME the test binary started with: the user's

// init names TMPDIR as the OS gives it back (macOS's /var is /private/var), so every t.TempDir in a binary that
// links this package compares equal to the paths git and the code under test read. On macOS it's /tmp: the per-user
// TMPDIR is long enough to cut a path in a frame and to pass the unix socket limit.
func init() {
	dir := os.TempDir()
	if runtime.GOOS == "darwin" {
		dir = "/tmp"
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		os.Setenv("TMPDIR", real)
	}
}

// Test gives t a temporary directory (TMPDIR) and, unless a harness already gave it one, a short HOME of its own, so
// nothing it keeps under HOME reaches the user's and a socket under it fits the unix limit; it puts the XDG directories back under that HOME, keeps the Go caches where they were, and fails it if it leaves
// goroutines running once its cleanups are done (leaktest.Check). A second call in the same test does nothing.
func Test(t testing.TB) {
	t.Helper()
	if _, dup := started.LoadOrStore(t.Name(), true); dup {
		return
	}
	t.Cleanup(func() { started.Delete(t.Name()) })
	leaktest.Check(t)
	keepGoCaches(t)
	t.Setenv("TMPDIR", t.TempDir())
	if os.Getenv("HOME") == userHome {
		t.Setenv("HOME", shortDir(t))
	}
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(v, "")
	}
}

// keepGoCaches names the Go caches the user's HOME gives, so a go command a test runs under its own HOME doesn't
// start them again empty.
func keepGoCaches(t testing.TB) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	gopath := cmp.Or(os.Getenv("GOPATH"), filepath.Join(home, "go"))
	cache := os.Getenv("GOCACHE")
	if cache == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(dir, "go-build")
		}
	}
	t.Setenv("GOPATH", gopath)
	t.Setenv("GOMODCACHE", cmp.Or(os.Getenv("GOMODCACHE"), filepath.Join(gopath, "pkg", "mod")))
	if cache != "" {
		t.Setenv("GOCACHE", cache)
	}
}

// shortDir is a directory of t's own under /tmp, removed when it ends: t.TempDir's paths are too long for a socket.
func shortDir(t testing.TB) string {
	tmp, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(tmp, "oh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
