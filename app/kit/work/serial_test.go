package work

import (
	"slices"
	"sync"
	"testing"
)

// What a Serial is handed runs in order, one at a time, and handing it over doesn't wait for it.
func TestSerialRunsInOrderWithoutWaiting(t *testing.T) {
	var s Serial
	defer s.Stop()
	var mu sync.Mutex
	var got []int
	gate := make(chan struct{})
	for i := range 5 { // the first holds the rest until the gate opens: a Do that waited would never return
		s.Queue(func() {
			if i == 0 {
				<-gate
			}
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
		})
	}
	close(gate)
	s.Wait()
	if !slices.Equal(got, []int{0, 1, 2, 3, 4}) {
		t.Errorf("ran %v", got)
	}
}
