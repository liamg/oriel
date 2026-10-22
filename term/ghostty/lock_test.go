package ghostty

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// A reader (the frame drawing the grid) waits on a 64KB feed for a slice of it, not the whole.
func TestBigWritesDontHoldReadersBack(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing")
	}
	e := mustNew(t, 120, 40, Options{})
	feed := mixedStream(64 << 10)
	start := time.Now()
	e.Write(feed)
	whole := time.Since(start)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				e.Write(feed)
			}
		}
	}()
	var waits []time.Duration
	for end := time.Now().Add(300 * time.Millisecond); time.Now().Before(end); {
		t0 := time.Now()
		e.Size()
		waits = append(waits, time.Since(t0))
		time.Sleep(200 * time.Microsecond)
	}
	close(stop)
	wg.Wait()
	slices.Sort(waits)
	p50, p99 := waits[len(waits)/2], waits[len(waits)*99/100]
	t.Logf("a 64KB feed takes %v; a reader waited %v typically, %v at the 99th percentile", whole, p50, p99)
	if p50 > whole/10 {
		t.Fatalf("a reader typically waited %v behind a %v feed", p50, whole)
	}
}
