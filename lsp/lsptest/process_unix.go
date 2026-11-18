//go:build unix

package lsptest

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

// Alive reports whether pid is a process that hasn't exited: one that has exited but not been reaped yet (a zombie)
// counts as gone.
func Alive(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false // ps exits non-zero for no such process
	}
	return !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
