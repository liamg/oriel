package typeset

import (
	"runtime"
	"testing"
	"time"
)

// Close stops the worker, and a rune asked for after it starts none.
func TestCloseStopsTheFallbackWorker(t *testing.T) {
	before := runtime.NumGoroutine()
	ts, err := NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	ts.fb.request('★')
	ts.Close()
	ts.fb.request('☂')
	for deadline := time.Now().Add(5 * time.Second); runtime.NumGoroutine() > before; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("%d goroutines, %d before", runtime.NumGoroutine(), before)
		}
	}
}
