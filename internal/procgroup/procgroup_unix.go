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

// Kill kills cmd's process group, then cmd itself in case it left the group.
func Kill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	return cmd.Process.Kill()
}
