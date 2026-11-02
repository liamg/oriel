package isolate

import (
	"os"
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
	cache, _ := os.UserCacheDir()
	var inner string
	t.Run("a", func(t *testing.T) {
		Test(t)
		inner, _ = os.UserHomeDir()
		if gc := os.Getenv("GOCACHE"); gc == "" || !strings.HasPrefix(gc, cache) {
			t.Errorf("GOCACHE %q, not under %s", gc, cache)
		}
	})
	if inner == outer || strings.HasPrefix(inner, outer+string(os.PathSeparator)+"Library") {
		t.Fatalf("HOME %s inside; %s outside", inner, outer)
	}
	if h, _ := os.UserHomeDir(); h != outer {
		t.Fatalf("HOME %s after; %s before", h, outer)
	}
}
