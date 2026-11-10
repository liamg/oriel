package shell

import (
	"fmt"
	"slices"
	"sync"
)

// inflight counts the work plugins run off the loop (plugin.Begin), by what it is.
type inflight struct {
	mu sync.Mutex
	n  map[string]int
}

func (f *inflight) begin(what string) func() {
	f.mu.Lock()
	if f.n == nil {
		f.n = map[string]int{}
	}
	f.n[what]++
	f.mu.Unlock()
	return func() {
		f.mu.Lock()
		if f.n[what]--; f.n[what] <= 0 {
			delete(f.n, what)
		}
		f.mu.Unlock()
	}
}

func (f *inflight) busy() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.n) > 0
}

// what names the work under way, with how many of each.
func (f *inflight) what() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k, n := range f.n {
		if n > 1 {
			k = fmt.Sprintf("%s ×%d", k, n)
		}
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Begin counts what as under way until end (plugin.Tracker).
func (h *pluginHost) Begin(what string) (end func()) {
	return h.sh.inflight.begin(h.source + ": " + what)
}
