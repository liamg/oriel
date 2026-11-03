//go:build unix

package update

import (
	"os"
	"syscall"
)

// Reexec replaces this process with exe, keeping its arguments, environment and pid.
func Reexec(exe string) error {
	return syscall.Exec(exe, append([]string{exe}, os.Args[1:]...), os.Environ())
}
