//go:build unix

package shell

import (
	"syscall"
	"time"
)

// cpuTime is the CPU time this process has used, user and system.
func cpuTime() time.Duration {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}
