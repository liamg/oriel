//go:build unix

package procgroup

import (
	"os/exec"
	"syscall"
)

// Own puts cmd, once started, in a process group of its own. Call it before cmd.Start.
func Own(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// Terminate asks cmd's process group to stop (SIGTERM), so a program that tidies up on it (git removing its lock
// files) can, and what it started stops with it.
func Terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	return cmd.Process.Signal(syscall.SIGTERM)
}

// Kill kills cmd's process group, then cmd itself in case it left the group.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return cmd.Process.Kill()
}
