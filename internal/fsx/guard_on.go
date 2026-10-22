//go:build oriel_framecheck

package fsx

import (
	"bytes"
	"runtime"
	"strconv"
	"sync"
)

// Checking says calls are checked: this is an oriel_framecheck build.
const Checking = true

// inFrame are the goroutines in a frame, by id: each window's loop while it builds a frame or runs its work.
var inFrame struct {
	mu sync.Mutex
	g  map[uint64]int
}

// Enter marks the calling goroutine as in a frame until leave: I/O through fsx on it panics meanwhile.
func Enter() (leave func()) {
	id := goid()
	inFrame.mu.Lock()
	if inFrame.g == nil {
		inFrame.g = map[uint64]int{}
	}
	inFrame.g[id]++
	inFrame.mu.Unlock()
	return func() {
		inFrame.mu.Lock()
		if inFrame.g[id]--; inFrame.g[id] <= 0 {
			delete(inFrame.g, id)
		}
		inFrame.mu.Unlock()
	}
}

func guard(what string) {
	id := goid()
	inFrame.mu.Lock()
	in := inFrame.g[id] > 0
	inFrame.mu.Unlock()
	if in {
		panic("fsx: " + what + " in a frame, on the loop")
	}
}

func goid() uint64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i >= 0 {
		b = b[:i]
	}
	id, _ := strconv.ParseUint(string(b), 10, 64)
	return id
}
