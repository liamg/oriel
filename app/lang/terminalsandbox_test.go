package lang_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
)

// imageBackend is rig.EchoBackend with an image: it describes itself, and a rebuild writes its log.
type imageBackend struct {
	rig.EchoBackend
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

// While the session in front is sandboxed, a terminal runs in its sandbox; term.host opens one on this machine.
func TestTerminalsRunInTheSandbox(t *testing.T) {
	s, _, _ := apptest.SandboxedScene(t, imageBackend{log: filepath.Join(t.TempDir(), "build.log")})
	s.Run("term.open", nil)
	s.Until("the sandbox's shell", func() bool { return s.Has("shell-in-the-sandbox", "sandbox") })
	s.Run("term.host", registry.Args{"cmd": []string{"sh", "-c", "echo on-the-host; exec cat"}})
	s.Until("the host's shell", func() bool { return s.Has("on-the-host") })
}

// heldShell is imageBackend whose shell answers once held closes.
type heldShell struct {
	imageBackend
	held chan struct{}
}

func (b heldShell) Shell(ctx context.Context, root, cwd string, o sandbox.LaunchOptions) (sandbox.Command, error) {
	<-b.held
	return b.imageBackend.Shell(ctx, root, cwd, o)
}

// A terminal in the sandbox opens off the loop: term.open answers while the sandbox's shell starts.
func TestSandboxTerminalOpensOffTheLoop(t *testing.T) {
	held := make(chan struct{})
	s, _, _ := apptest.SandboxedScene(t, heldShell{imageBackend{log: filepath.Join(t.TempDir(), "build.log")}, held})
	ran := make(chan error, 1)
	go func() { ran <- s.Registry().Run(context.Background(), "term.open", nil) }()
	release := sync.OnceFunc(func() { close(held) })
	t.Cleanup(release)
	if err := apptest.Recv(t, "term.open to answer while the sandbox's shell starts", ran); err != nil {
		t.Fatal(err)
	}
	release()
	s.Until("the sandbox's shell", func() bool { return s.Has("shell-in-the-sandbox", "sandbox") })
}
