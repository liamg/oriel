package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/sandbox/container"
)

// An image's passwd and group come from one container the first time, and
// from the cache after that, until the image changes.
func TestImageUsersReadOncePerImage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	runs := filepath.Join(dir, "runs")
	fake := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1 $2" in
  "image inspect") cat "` + dir + `/id" ;;
  run*) echo run >> "` + runs + `"; printf 'root:x:0:0::/root:/bin/sh\n\0root:x:0:\n' ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := container.Bin
	container.Bin = fake
	t.Cleanup(func() { container.Bin = old })
	setID := func(id string) { _ = os.WriteFile(filepath.Join(dir, "id"), []byte(id+"\n"), 0o644) }
	count := func() int { b, _ := os.ReadFile(runs); return strings.Count(string(b), "run") }

	setID("sha256:aaa")
	for range 3 {
		p, g, err := imageUsers(context.Background(), "img")
		if err != nil || !strings.HasPrefix(p, "root:x:0:0") || !strings.HasPrefix(g, "root:x:0:") {
			t.Fatalf("got %q %q %v", p, g, err)
		}
	}
	if n := count(); n != 1 {
		t.Fatalf("three launches on one image ran %d containers, want 1", n)
	}
	setID("sha256:bbb")
	if _, _, err := imageUsers(context.Background(), "img"); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 2 {
		t.Fatalf("a rebuilt image was not read again: %d runs", n)
	}
}
