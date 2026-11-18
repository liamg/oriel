package hostcore

import (
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/liamg/oriel/internal/chaos"
)

// Jobs are the host's own goroutines (Env.Go), by what each is doing, so closing can wait for them and say which it's
// still waiting on. A job that lasts as long as what it serves (an agent's event loop, a sign-in) says so as it starts
// (GoLasting), so a wait for the host's work to settle passes over it.
type Jobs struct {
	mu      sync.Mutex
	running map[job]int
	wg      sync.WaitGroup
}

type job struct {
	name    string
	lasting bool
}

// Go runs fn as a job named where, one that ends on its own.
func (j *Jobs) Go(where string, fn func()) { j.start(job{where, false}, fn) }

// GoLasting runs fn as a job named where that lasts as long as what it serves, so never ends on its own.
func (j *Jobs) GoLasting(where string, fn func()) { j.start(job{where, true}, fn) }

func (j *Jobs) start(k job, fn func()) {
	j.mu.Lock()
	if j.running == nil {
		j.running = map[job]int{}
	}
	j.running[k]++
	j.wg.Add(1)
	j.mu.Unlock()
	go func() {
		defer func() {
			j.mu.Lock()
			if j.running[k]--; j.running[k] == 0 {
				delete(j.running, k)
			}
			j.mu.Unlock()
			j.wg.Done()
		}()
		chaos.Sleep()
		fn()
	}()
}

// Wait waits for every job to end.
func (j *Jobs) Wait() { j.wg.Wait() }

// Running are the jobs not ended yet, by name, with how many of each: "a sign-in", "a btw ×2".
func (j *Jobs) Running() []string { return j.list(func(job) bool { return true }) }

// Transient are the jobs not ended yet that will end on their own: Running less the lasting.
func (j *Jobs) Transient() []string { return j.list(func(k job) bool { return !k.lasting }) }

func (j *Jobs) list(keep func(job) bool) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	counts := map[string]int{}
	for k, n := range j.running {
		if keep(k) {
			counts[k.name] += n
		}
	}
	var out []string
	for _, name := range slices.Sorted(maps.Keys(counts)) {
		if n := counts[name]; n > 1 {
			out = append(out, fmt.Sprintf("%s ×%d", name, n))
		} else {
			out = append(out, name)
		}
	}
	return out
}
