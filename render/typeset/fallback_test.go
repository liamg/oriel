package typeset

import (
	"testing"
)

// Close stops the worker, and a rune asked for after it starts none.
func TestCloseStopsTheFallbackWorker(t *testing.T) {
	ts, err := NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	ts.fb.request('★')
	ts.Close()
	ts.fb.request('☂')
	<-ts.fb.done
	ts.fb.mu.Lock()
	defer ts.fb.mu.Unlock()
	if ts.fb.requested['☂'] {
		t.Error("a rune asked for after Close went to the worker")
	}
}
