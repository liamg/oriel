package ossandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Available says whether sandbox-exec is here.
func Available() error {
	if _, err := os.Stat(sandboxExec); err != nil {
		return fmt.Errorf("%w: no %s", ErrUnavailable, sandboxExec)
	}
	return nil
}

// Main does nothing on macOS: sandbox-exec applies the profile itself.
func Main() {}

func command(p Profile, path string, args []string) (*exec.Cmd, error) {
	for _, l := range []*[]string{&p.Read, &p.Write, &p.Exec} {
		*l = realPaths(*l)
	}
	return exec.Command(sandboxExec, append([]string{"-p", SBPL(p), path}, args...)...), nil
}

// realPaths are paths with their symlinks followed, as Seatbelt matches them: /tmp is /private/tmp.
func realPaths(ps []string) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			p = r
		}
		out = append(out, p)
	}
	return out
}
