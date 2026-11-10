package kit

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// What a Serial is handed runs in order, one at a time, and handing it over doesn't wait for it.
func TestSerialRunsInOrderWithoutWaiting(t *testing.T) {
	var s Serial
	defer s.Stop()
	var mu sync.Mutex
	var got []int
	gate := make(chan struct{})
	handed := make(chan struct{})
	go func() {
		defer close(handed)
		for i := range 5 {
			s.Do(func() {
				if i == 0 {
					<-gate
				}
				mu.Lock()
				got = append(got, i)
				mu.Unlock()
			})
		}
	}()
	select {
	case <-handed: // the first is held at the gate: handing over waited on nothing
	case <-time.After(10 * time.Second):
		t.Fatal("handing work over waited for the work")
	}
	close(gate)
	s.Wait()
	if !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("ran %v", got)
	}
}
