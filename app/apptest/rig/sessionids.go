package rig

import (
	"fmt"
	"sync/atomic"
)

// SessionIDs is a host's session ids for a test: s0000001, s0000002… in the order the sessions start, so what a test
// draws is the same each run.
func SessionIDs() func() string {
	var n atomic.Int64
	return func() string { return fmt.Sprintf("s%07d", n.Add(1)) }
}
