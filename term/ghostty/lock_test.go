package ghostty

import (
	goruntime "runtime"
	"sync/atomic"
	"testing"
)

// A reader (the frame drawing the grid) waiting on a big write gets the terminal between two of its slices, not
// once the whole write is in: checked by the order the two finish in, recorded under the terminal's lock.
func TestAWaitingReaderGetsInBeforeABigWriteEnds(t *testing.T) {
	e := mustNew(t, 120, 40, Options{})
	feed := mixedStream(16 * slice)
	var seq, read, wrote atomic.Int64
	readDone, writeDone := make(chan struct{}), make(chan struct{})
	e.mu.Lock() // the writer and the reader both queue behind this
	go func() {
		defer close(writeDone)
		e.Write(feed)
		wrote.Store(seq.Add(1))
	}()
	go func() {
		defer close(readDone)
		e.lock()
		read.Store(seq.Add(1)) // under the lock: the writer can't move meanwhile
		e.mu.Unlock()
	}()
	for e.waiting.Load() == 0 {
		goruntime.Gosched() // until the reader is queued
	}
	e.mu.Unlock()
	<-readDone
	<-writeDone
	if read.Load() > wrote.Load() {
		t.Errorf("the reader got in only once the whole %d-slice write was done", len(feed)/slice)
	}
}
