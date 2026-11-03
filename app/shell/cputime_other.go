//go:build !unix

package shell

import "time"

// cpuTime is the CPU time this process has used; none known here, so a stall always counts as Oriel's.
func cpuTime() time.Duration { return 0 }
