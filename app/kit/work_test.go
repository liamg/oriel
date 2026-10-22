package kit

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestWorkStopWaitsForItsGoroutines(t *testing.T) {
	var w Work
	var ended atomic.Bool
	started := make(chan struct{})
	w.Go(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		ended.Store(true)
	})
	<-started
	w.Stop()
	if !ended.Load() {
		t.Fatal("Stop returned before its goroutine")
	}
	ran := false
	w.Go(func(context.Context) { ran = true })
	w.Stop()
	if ran {
		t.Error("Go after Stop ran")
	}
}
