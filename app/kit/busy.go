package kit

import (
	"sync"

	"github.com/liamg/oriel/plugin"
)

// Counter counts work started and finished: a Busy or a sync.WaitGroup.
type Counter interface {
	Add(n int)
	Done()
}

// Busy counts work running off the loop, like a sync.WaitGroup that may be waited on while more is added: a test
// waits for a plugin's work as frames start more.
type Busy struct {
	mu   sync.Mutex
	cond *sync.Cond
	n    int
}

// Add counts n more.
func (b *Busy) Add(n int) {
	b.mu.Lock()
	b.n += n
	if b.n < 0 {
		b.mu.Unlock()
		panic("kit: negative Busy count")
	}
	if b.n == 0 && b.cond != nil {
		b.cond.Broadcast()
	}
	b.mu.Unlock()
}

// Done counts one finished.
func (b *Busy) Done() { b.Add(-1) }

// Wait returns once none is running.
func (b *Busy) Wait() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cond == nil {
		b.cond = sync.NewCond(&b.mu)
	}
	for b.n > 0 {
		b.cond.Wait()
	}
}

// Busy says some work is running: what a plugin hands a registry.Busy.
func (b *Busy) Busy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n > 0
}

// Go runs f on a goroutine of its own, counted while it runs.
func (b *Busy) Go(f func()) {
	b.Add(1)
	go func() {
		defer b.Done()
		plugin.Chaos()
		f()
	}()
}
