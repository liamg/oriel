package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// describeEngine is a CLI whose container runs with two mounts, and whose images all exist.
func describeEngine(t *testing.T) container.Engine {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := `#!/bin/sh
case "$1 $2" in
"container inspect") echo 'true [{"Source":"/w","Destination":"/w","RW":true},{"Source":"/c","Destination":"/a/c","RW":false}]';;
"image inspect") echo sha256:1;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return container.Engine{Bin: bin}
}

// A workspace with no Dockerfile of its own runs the base image; its description says so, with what the running
// container mounts, where it puts each host path it mounts elsewhere, and its last build's log.
func TestDescribeTheBaseImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := container.WithEngine(context.Background(), describeEngine(t))
	root := t.TempDir()
	tag := container.BaseImage()
	os.MkdirAll(filepath.Dir(BuildLog(tag)), 0o700)
	os.WriteFile(BuildLog(tag), []byte("step 1\n"), 0o600)
	d := Describe(ctx, root, sandbox.Config{})
	if d.Image != tag || d.ImageFrom != "the base image" || d.Dockerfile != container.BaseDockerfile || !d.Built || !d.Running {
		t.Errorf("description = %+v", d)
	}
	if d.Container != Name(root) || d.BuildLog != BuildLog(tag) {
		t.Errorf("container %q, log %q", d.Container, d.BuildLog)
	}
	want := []sandbox.Bind{{Host: "/c", Agent: "/a/c", ReadOnly: true}, {Host: "/w", Agent: "/w"}}
	if len(d.Mounts) != 2 || d.Mounts[0] != want[0] || d.Mounts[1] != want[1] {
		t.Errorf("mounts = %+v, want %+v", d.Mounts, want)
	}
	if d.Places[sandbox.HomeDir(root)] == "" || d.Places["/c"] != "/a/c" {
		t.Errorf("no place for the home, or for a mount elsewhere (an agent's scratchpad): %v", d.Places)
	}
}

// An image named in settings isn't Oriel's to build.
func TestRebuildAnImageFromSettings(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := Rebuild(context.Background(), t.TempDir(), sandbox.Config{Image: "mine:1"})
	if err == nil || !strings.Contains(err.Error(), "mine:1") {
		t.Errorf("rebuilding a settings image: %v", err)
	}
}

// A terminal's shell runs in the agent's container while it runs, as the agent does; with none running there's
// nothing to run it in.
func TestShellExecsIntoTheRunningContainer(t *testing.T) {
	e := describeEngine(t)
	root := t.TempDir()
	argv, err := Shell(context.Background(), e, root, root+"/repos/api")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	for _, want := range []string{" exec -i -t ", "-w " + root + "/repos/api ", Name(root) + " sh -c "} {
		if !strings.Contains(got, want) {
			t.Errorf("argv = %s, want %q in it", got, want)
		}
	}
	stopped := container.Engine{Bin: "false"}
	if _, err := Shell(context.Background(), stopped, root, root); err == nil {
		t.Error("a shell with no container running")
	}
}
