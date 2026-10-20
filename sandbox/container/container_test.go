package container

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A one-off install has no terminal to attach: no -t, so its output can go
// to a log, and --rm so it leaves nothing behind.
func TestOnceArgv(t *testing.T) {
	args := Spec{Name: "x", Image: "img"}.OnceArgv(Docker, "/env")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, " -t") || !strings.Contains(joined, "--rm") || !strings.Contains(joined, "--env-file /env") {
		t.Errorf("args = %s", joined)
	}
	if args[0] != "docker" || args[1] != "run" {
		t.Errorf("args = %q, want docker run …", args)
	}
}

// An engine with a socket of its own names it on every command, whatever the CLI's current context.
func TestEngineHost(t *testing.T) {
	e := Engine{Bin: "docker", Host: "/home/u/.orbstack/run/docker.sock"}
	got := strings.Join(Spec{Name: "x", Image: "img"}.InteractiveArgv(e, ""), " ")
	if !strings.HasPrefix(got, "docker -H unix:///home/u/.orbstack/run/docker.sock run ") {
		t.Errorf("args = %s", got)
	}
}

func TestLimits(t *testing.T) {
	for _, tc := range []struct {
		spec Spec
		want []string
		not  string
	}{
		{Spec{}, []string{"--memory 8g", "--pids-limit 4096"}, ""},
		{Spec{Memory: "2g", PidsLimit: 100}, []string{"--memory 2g", "--pids-limit 100"}, ""},
		{Spec{Memory: "none", PidsLimit: -1}, []string{"--pids-limit -1"}, "--memory"},
	} {
		args := strings.Join(tc.spec.InteractiveArgv(Docker, ""), " ")
		for _, w := range tc.want {
			if !strings.Contains(args, w) {
				t.Errorf("%+v: no %q in %s", tc.spec, w, args)
			}
		}
		if tc.not != "" && strings.Contains(args, tc.not) {
			t.Errorf("%+v: %q in %s", tc.spec, tc.not, args)
		}
	}
}

// fakeEngine is a CLI that prints its args and the Dockerfile on stdin, as a build's output.
func fakeEngine(t *testing.T) Engine {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\necho \"args: $*\"\ncase \"$1\" in build) cat; echo 'step 1/1 done' >&2;; image) exit 1;; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Engine{Bin: bin}
}

// A build's output goes to its log, which the next build of the tag replaces; a rebuild skips the cache.
func TestBuildKeepsItsLog(t *testing.T) {
	ctx := WithEngine(context.Background(), fakeEngine(t))
	log := filepath.Join(t.TempDir(), "builds", "img.log")
	if err := EnsureImage(ctx, "img:1", "FROM scratch\n", log); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"build", "FROM scratch", "step 1/1 done"} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the log has no %q:\n%s", want, b)
		}
	}
	if strings.Contains(string(b), "--no-cache") {
		t.Errorf("a first build skipped the cache:\n%s", b)
	}
	if err := Rebuild(ctx, "img:1", "FROM scratch\nRUN true\n", log); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(log)
	if !strings.Contains(string(b), "--no-cache") || !strings.Contains(string(b), "RUN true") || strings.Count(string(b), "args:") != 1 {
		t.Errorf("the rebuild's log isn't its own, or used the cache:\n%s", b)
	}
}
