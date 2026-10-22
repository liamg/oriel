//go:build darwin && cgo

package fswatch

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFSEventsFlags(t *testing.T) {
	for ours, apple := range cFlags {
		if ours != apple {
			t.Errorf("flag %#x, FSEvents.h has %#x", ours, apple)
		}
	}
}

func TestTreeIsOneStream(t *testing.T) {
	root := t.TempDir()
	for i := range 200 {
		write(t, filepath.Join(root, fmt.Sprint("d", i), "f.txt"), "x")
	}
	w := start(t)
	if err := w.AddTree(root, nil); err != nil {
		t.Fatal(err)
	}
	if got := w.b.watched(); len(got) != 1 {
		t.Fatalf("%d kqueue watches for a tree, want 1 (the root)", len(got))
	}
	p := filepath.Join(root, "d7", "f.txt")
	write(t, p, "more")
	expect(t, w, p)
	if err := os.Mkdir(filepath.Join(root, "new"), 0o755); err != nil {
		t.Fatal(err)
	}
	quiet(w, 200*time.Millisecond)
	q := filepath.Join(root, "new", "g.txt")
	write(t, q, "x")
	expect(t, w, q)
}
