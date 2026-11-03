package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// Building an image says so, and that it's a one-off; an image already built says nothing.
func TestImageBuildReportsProgress(t *testing.T) {
	dir := t.TempDir()
	built := filepath.Join(dir, "built")
	fake := filepath.Join(dir, "docker")
	script := `#!/bin/sh
case "$1 $2" in
  "image inspect") [ -f "` + built + `" ] && echo sha256:aaa || exit 1 ;;
  build*) cat >/dev/null; touch "` + built + `" ;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := container.Engine{Bin: fake}

	var got []sandbox.Progress
	ctx := sandbox.WithProgress(container.WithEngine(context.Background(), engine), func(p sandbox.Progress) { got = append(got, p) })
	if err := ensureImage(ctx, "img:1", "FROM scratch\n"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Step != "Building the sandbox image" || got[0].Note == "" {
		t.Fatalf("a build reported %+v", got)
	}
	got = nil
	if err := ensureImage(ctx, "img:1", "FROM scratch\n"); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("a built image reported %+v", got)
	}
}
