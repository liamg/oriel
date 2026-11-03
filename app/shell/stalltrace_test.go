package shell_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/shell"
)

// The loop marks a stall without waiting on a trace still being written.
func TestStallTraceNeverWaitsOnAWrite(t *testing.T) {
	s, ok := shell.StartStallTrace(filepath.Join(t.TempDir(), "stall.trace"))
	if !ok {
		t.Skip("no flight recorder")
	}
	defer s.Stop()
	s.HoldWrite() // a write in progress
	done := make(chan struct{})
	go func() {
		s.Stalled()
		s.Forget()
		s.Stalled()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("stalled waited on the write")
	}
	s.ReleaseWrite()
}
