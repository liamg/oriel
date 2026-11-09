package hostcore

import (
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Jobs are the host's own goroutines (Env.Go), by what each is doing, so closing can wait for them and say which it's
// still waiting on.
type Jobs struct {
	mu      sync.Mutex
	running map[string]int
	wg      sync.WaitGroup
}

// Go runs fn as a job named where.
func (j *Jobs) Go(where string, fn func()) {
	j.mu.Lock()
	if j.running == nil {
		j.running = map[string]int{}
	}
	j.running[where]++
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer func() {
			j.mu.Lock()
			if j.running[where]--; j.running[where] == 0 {
				delete(j.running, where)
			}
			j.mu.Unlock()
			j.wg.Done()
		}()
		fn()
	}()
}

// Wait waits for every job to end.
func (j *Jobs) Wait() { j.wg.Wait() }

// Running are the jobs not ended yet, by name, with how many of each: "a sign-in", "a btw ×2".
func (j *Jobs) Running() []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []string
	for _, name := range slices.Sorted(maps.Keys(j.running)) {
		if n := j.running[name]; n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", name, n))
		} else {
			out = append(out, name)
		}
	}
	return out
}
