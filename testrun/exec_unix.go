//go:build unix

package testrun

import (
	"os/exec"
	"syscall"
)

// killGroup runs the command in a process group of its own, and stops the whole group when the run is cancelled:
// go test and npx start children of their own.
func killGroup(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
}
