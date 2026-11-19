//go:build !unix

package procgroup

import "os/exec"

// Own does nothing where there are no process groups.
func Own(cmd *exec.Cmd) {}

// Terminate kills cmd, as there's no gentler stop to ask for.
func Terminate(cmd *exec.Cmd) error { return Kill(cmd) }

// Kill kills cmd.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return cmd.Process.Kill()
}
