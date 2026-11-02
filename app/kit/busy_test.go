package kit

import (
	"sync"
	"testing"
	"time"
)

// Busy can be waited on while more is added, which a sync.WaitGroup panics at.
func TestBusyWaitsWhileMoreIsAdded(t *testing.T) {
	var b Busy
	stop := make(chan struct{})
	var adders sync.WaitGroup
	adders.Add(1)
	go func() {
		defer adders.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			b.Add(1)
			go b.Done()
		}
	}()
	for range 1000 {
		b.Wait()
	}
	close(stop)
	adders.Wait()
	done := make(chan struct{})
	go func() { b.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait never returned with nothing running")
	}
}
