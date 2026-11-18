//go:build !unix

package procgroup

import "os/exec"

// Own does nothing where there are no process groups.
func Own(cmd *exec.Cmd) {}

// Kill kills cmd.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
