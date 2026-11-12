//go:build unix

package sysload

import "syscall"

// Kill ends each of ps at once.
func Kill(ps []Proc) {
	for _, p := range ps {
		_ = syscall.Kill(p.PID, syscall.SIGKILL)
	}
}
