package isolate

import (
	"os"
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
