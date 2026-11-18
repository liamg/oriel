package ossandbox

import (
	"fmt"
	"os/exec"

	"github.com/liamg/oriel/sandbox/sbpl"
)

// Available says whether sandbox-exec is here.
func Available() error {
	if err := sbpl.Available(); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return nil
}

// Main does nothing on macOS: sandbox-exec applies the profile itself.
func Main() {}

func command(p Profile, path string, args []string) (*exec.Cmd, error) {
	for _, l := range []*[]string{&p.Read, &p.Write, &p.Exec} {
		*l = realPaths(*l)
	}
	return exec.Command(sbpl.SandboxExec, append([]string{"-p", SBPL(p), path}, args...)...), nil
}

// realPaths are paths with their symlinks followed, as Seatbelt matches them: /tmp is /private/tmp.
func realPaths(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, sbpl.Real(p))
	}
	return out
}
