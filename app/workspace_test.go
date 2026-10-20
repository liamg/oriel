package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
)

// imageBackend is testBackend with an image: it describes itself, and a rebuild writes its log.
type imageBackend struct {
	testBackend
	log string
}

func (b imageBackend) Describe(_ context.Context, root string, _ sandbox.Config) sandbox.Description {
	d := sandbox.Description{Container: "oriel-test", Running: true, Image: "oriel-sandbox:abc", ImageFrom: "the base image",
		Dockerfile: "FROM debian:bookworm-slim\nRUN apt-get install -y ripgrep\n", Built: true,
		Places: map[string]string{sandbox.TmpDir(root): "/tmp/claude-501"}}
	if _, err := os.Stat(b.log); err == nil {
		d.BuildLog = b.log
	}
	return d
}

func (imageBackend) Shell(_ context.Context, _, cwd string, _ sandbox.LaunchOptions) (sandbox.Command, error) {
	return sandbox.Command{Argv: []string{"sh", "-c", "echo shell-in-the-sandbox; exec cat"}, Env: os.Environ()}, nil
}

func (b imageBackend) Rebuild(context.Context, string, sandbox.Config) error {
	return os.WriteFile(b.log, []byte("#1 [internal] load build definition\n#5 DONE 2.1s\n"), 0o600)
}

// The Workspace page shows where the agent works and where it finds each directory, browses the scratchpad, shows
// the image's Dockerfile and rebuilds it into the build log, and holds the Permissions.
func TestWorkspacePage(t *testing.T) {
	b := imageBackend{log: filepath.Join(t.TempDir(), "build.log")}
	r, _, se := sandboxedSessionOn(t, b)
	root := se.Dir
	if p, ok := se.Primary(); root == "" && ok {
		root = p.Dir
	}
	os.MkdirAll(sandbox.TmpDir(root), 0o700)
	if err := os.WriteFile(filepath.Join(sandbox.TmpDir(root), "notes.md"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := r.do(func() error { return r.host.Run(context.Background(), "workspace.open", nil) }); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the overview", func(s string) bool {
		return containsAll(s, "SESSION", "SANDBOX", "DIRECTORIES", "Scratchpad", "in the sandbox: /tmp/claude-501", "oriel-test · running",
			"oriel-sandbox:abc · the base image", "TEST · RUNNING")
	})

	r.clickText("FILES")
	r.waitText("notes.md")

	r.clickText("IMAGE")
	r.waitText("RUN apt-get install -y ripgrep")
	r.clickText("Rebuild image")
	r.waitText("#5 DONE 2.1s")

	r.clickText("PERMISSIONS")
	r.waitFrame("the permissions", func(s string) bool { return containsAll(s, "TOOLS", "NETWORK") })

	if err := r.do(func() error {
		return r.host.Run(context.Background(), "workspace.open", registry.Args{"tab": "nothing"})
	}); err == nil {
		t.Error("opened a tab that isn't there")
	}
}

// While the session in front is sandboxed, a terminal runs in its sandbox; term.host opens one on this machine.
func TestTerminalsRunInTheSandbox(t *testing.T) {
	r, _, _ := sandboxedSessionOn(t, imageBackend{log: filepath.Join(t.TempDir(), "build.log")})
	if err := r.do(func() error { return r.host.Run(context.Background(), "term.open", nil) }); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the sandbox's shell", func(s string) bool { return containsAll(s, "shell-in-the-sandbox", "sandbox") })
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "term.host", registry.Args{"cmd": []string{"sh", "-c", "echo on-the-host; exec cat"}})
	}); err != nil {
		t.Fatal(err)
	}
	r.waitText("on-the-host")
}
