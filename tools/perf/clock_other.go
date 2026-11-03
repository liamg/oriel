//go:build !unix

package perf

import "time"

// threadTime is the wall clock: this platform's thread CPU clock isn't read.
func threadTime() time.Duration { return time.Duration(time.Now().UnixNano()) }
