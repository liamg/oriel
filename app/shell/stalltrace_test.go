package shell_test

import (
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/app/apptest"
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
	apptest.Recv(t, "stalled not to wait on the write", done)
	s.ReleaseWrite()
}
