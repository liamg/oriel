package container

import (
	"strings"
	"testing"
)

// A one-off install has no terminal to attach: no -t, so its output can go
// to a log, and --rm so it leaves nothing behind.
func TestOnceArgv(t *testing.T) {
	args := Spec{Name: "x", Image: "img"}.OnceArgv("/env")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, " -t") || !strings.Contains(joined, "--rm") || !strings.Contains(joined, "--env-file /env") {
		t.Errorf("args = %s", joined)
	}
	if args[0] != Bin {
		t.Errorf("args[0] = %q, want %q", args[0], Bin)
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
		args := strings.Join(tc.spec.InteractiveArgv(""), " ")
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
