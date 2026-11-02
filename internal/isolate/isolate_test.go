package isolate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestGivesItsOwnTempDir(t *testing.T) {
	outer := os.TempDir()
	var inner string
	t.Run("a", func(t *testing.T) {
		Test(t)
		inner = os.TempDir()
		Test(t)
		if os.TempDir() != inner {
			t.Fatalf("a second call moved TMPDIR from %s to %s", inner, os.TempDir())
		}
	})
	if inner == outer || os.TempDir() != outer {
		t.Fatalf("TMPDIR %s inside, %s after; %s before", inner, os.TempDir(), outer)
	}
}

// A test's HOME is its own, so state kept under it (Application Support, ~/.local/state) never reaches the user's;
// the Go caches stay where they were.
func TestTestGivesItsOwnHome(t *testing.T) {
	outer, _ := os.UserHomeDir()
	want := os.Getenv("GOCACHE")
	if want == "" {
		cache, _ := os.UserCacheDir()
		want = filepath.Join(cache, "go-build")
	}
	var inner string
	t.Run("a", func(t *testing.T) {
		Test(t)
		inner, _ = os.UserHomeDir()
		if gc := os.Getenv("GOCACHE"); gc != want {
			t.Errorf("GOCACHE %q, want %q", gc, want)
		}
	})
	if inner == outer || strings.HasPrefix(inner, outer+string(os.PathSeparator)+"Library") {
		t.Fatalf("HOME %s inside; %s outside", inner, outer)
	}
	if h, _ := os.UserHomeDir(); h != outer {
		t.Fatalf("HOME %s after; %s before", h, outer)
	}
}

// A test's temporary directories and HOME are named as git and the OS give them back (macOS's /var is
// /private/var), so a path a test builds compares equal to one the code under test reads.
func TestTestsPathsHaveNoSymlinks(t *testing.T) {
	t.Run("a", func(t *testing.T) {
		Test(t)
		home, _ := os.UserHomeDir()
		for _, p := range []string{t.TempDir(), os.TempDir(), home} {
			if real, err := filepath.EvalSymlinks(p); err != nil || real != filepath.Clean(p) {
				t.Errorf("%s resolves to %s (%v)", p, real, err)
			}
		}
	})
}
