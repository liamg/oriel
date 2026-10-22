//go:build unix

package perf

import (
	"time"

	"golang.org/x/sys/unix"
)

// threadTime is the CPU time the calling OS thread has used, or the wall clock where that can't be read.
func threadTime() time.Duration {
	var ts unix.Timespec
	if unix.ClockGettime(unix.CLOCK_THREAD_CPUTIME_ID, &ts) != nil {
		return time.Duration(time.Now().UnixNano())
	}
	return time.Duration(ts.Nano())
}
