package testrun

import (
	"encoding/json"
	"io"
	"sort"
	"sync"
	"time"
)

// Entry is one test's outcome in one run, as history keeps it.
type Entry struct {
	Run      string        `json:"run"`
	Status   Status        `json:"status"`
	Duration time.Duration `json:"duration,omitempty"`
	Time     time.Time     `json:"time"`
	Change   string        `json:"change,omitempty"`
	By       By            `json:"by,omitempty"`
}

// History is the last N runs, and the last N outcomes of every test, for the tests panel: sparklines, flaky
// tests, failed and slowest filters. It's safe for concurrent use.
type History struct {
	mu    sync.Mutex
	n     int
	runs  []*Run
	tests map[string][]Entry
}

// NewHistory keeps n runs and n outcomes per test.
func NewHistory(n int) *History {
	if n <= 0 {
		n = 20
	}
	return &History{n: n, tests: map[string][]Entry{}}
}

// Add records a finished run. A test it didn't run keeps its earlier outcomes.
func (h *History) Add(r *Run) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.runs = append(h.runs, r)
	if len(h.runs) > h.n {
		h.runs = h.runs[len(h.runs)-h.n:]
	}
	for _, x := range r.Tests {
		if x.Status == StatusRunning || x.Status == StatusCanceled {
			continue
		}
		k := x.Key()
		e := append(h.tests[k], Entry{Run: r.ID, Status: x.Status, Duration: x.Duration, Time: r.Start, Change: r.Change, By: r.By})
		if len(e) > h.n {
			e = e[len(e)-h.n:]
		}
		h.tests[k] = e
	}
}

// Runs is the runs kept, oldest first.
func (h *History) Runs() []*Run {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*Run(nil), h.runs...)
}

// Run finds a kept run by id: what an agent's "tests pass" links to.
func (h *History) Run(id string) (*Run, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, r := range h.runs {
		if r.ID == id {
			return r, true
		}
	}
	return nil, false
}

// Last is the latest run, optionally only those by one kind of runner ("" for any).
func (h *History) Last(by By) (*Run, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := len(h.runs) - 1; i >= 0; i-- {
		if by == "" || h.runs[i].By == by {
			return h.runs[i], true
		}
	}
	return nil, false
}

// Entries is a test's kept outcomes, oldest first: its sparkline.
func (h *History) Entries(key string) []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Entry(nil), h.tests[key]...)
}

// Latest is a test's most recent outcome.
func (h *History) Latest(key string) (Entry, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.tests[key]
	if len(e) == 0 {
		return Entry{}, false
	}
	return e[len(e)-1], true
}

// Flaky reports whether a test is flaky: it failed and then passed on a re-run of the same code, or its kept
// outcomes go from pass to fail and back (or the other way) more than once.
func (h *History) Flaky(key string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return flaky(h.tests[key])
}

func flaky(es []Entry) bool {
	flips := 0
	var prev *Entry
	for i := range es {
		e := &es[i]
		if e.Status != StatusPassed && !e.Status.Failing() {
			continue
		}
		if prev != nil {
			if prev.Status.Failing() && e.Status == StatusPassed && e.Change != "" && e.Change == prev.Change {
				return true
			}
			if prev.Status.Failing() != e.Status.Failing() {
				flips++
			}
		}
		prev = e
	}
	return flips >= 2
}

// FlakyTests is every flaky test's key, sorted.
func (h *History) FlakyTests() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k, es := range h.tests {
		if flaky(es) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Failing is the tests whose latest outcome is a failure, sorted.
func (h *History) Failing() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for k, es := range h.tests {
		if len(es) > 0 && es[len(es)-1].Status.Failing() {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Slowest is the n tests whose latest run took longest, slowest first; every test when n <= 0.
func (h *History) Slowest(n int) []string {
	h.mu.Lock()
	type kd struct {
		k string
		d time.Duration
	}
	var all []kd
	for k, es := range h.tests {
		if len(es) > 0 {
			all = append(all, kd{k, es[len(es)-1].Duration})
		}
	}
	h.mu.Unlock()
	sort.Slice(all, func(i, j int) bool {
		if all[i].d != all[j].d {
			return all[i].d > all[j].d
		}
		return all[i].k < all[j].k
	})
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	out := make([]string, len(all))
	for i, x := range all {
		out[i] = x.k
	}
	return out
}

type historyFile struct {
	N     int                `json:"n"`
	Runs  []*Run             `json:"runs"`
	Tests map[string][]Entry `json:"tests"`
}

// Save writes the history as JSON.
func (h *History) Save(w io.Writer) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(historyFile{N: h.n, Runs: h.runs, Tests: h.tests})
}

// LoadHistory reads a history Save wrote.
func LoadHistory(r io.Reader) (*History, error) {
	var f historyFile
	if err := json.NewDecoder(r).Decode(&f); err != nil {
		return nil, err
	}
	h := NewHistory(f.N)
	h.runs = f.Runs
	if f.Tests != nil {
		h.tests = f.Tests
	}
	return h, nil
}
