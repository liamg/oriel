package container

import (
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
