package testrun

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os/exec"
	"sort"
	"time"
)

// By is who started a run.
type By string

const (
	ByUser  By = "user"
	ByAgent By = "agent"
	ByWatch By = "watch"
)

// Status is how a test, a package or a run went.
type Status string

const (
	StatusRunning     Status = "running"
	StatusPassed      Status = "passed"
	StatusFailed      Status = "failed"
	StatusSkipped     Status = "skipped"
	StatusTimedOut    Status = "timed-out"
	StatusBuildFailed Status = "build-failed"
	StatusCanceled    Status = "canceled"
	StatusError       Status = "error" // the runner itself failed: it wouldn't start, or its output didn't parse
)

// Failing reports whether s is a failure of the code under test.
func (s Status) Failing() bool {
	return s == StatusFailed || s == StatusTimedOut || s == StatusBuildFailed
}

// Result is one test's or package's outcome in a run.
type Result struct {
	Package  string        `json:"package"`
	Test     string        `json:"test,omitempty"`
	Status   Status        `json:"status"`
	Duration time.Duration `json:"duration,omitempty"`
	Output   []string      `json:"output,omitempty"`
	Failure  *Failure      `json:"failure,omitempty"`
	Message  string        `json:"message,omitempty"` // a skip's reason
}

// Key identifies the result's test.
func (r *Result) Key() string { return Key(r.Package, r.Test) }

// Run is one run of tests: who started it and why, and what each test did.
type Run struct {
	ID       string    `json:"id"`
	Trigger  string    `json:"trigger,omitempty"`
	By       By        `json:"by,omitempty"`
	Agent    string    `json:"agent,omitempty"` // the session, for an agent's run
	Change   string    `json:"change,omitempty"`
	Mode     string    `json:"mode,omitempty"`
	Adapter  string    `json:"adapter,omitempty"`
	Args     []string  `json:"args,omitempty"`
	Scope    []string  `json:"scope,omitempty"` // the keys of the items asked for; empty for everything
	Start    time.Time `json:"start"`
	End      time.Time `json:"end,omitzero"`
	Status   Status    `json:"status"`
	Tests    []*Result `json:"tests,omitempty"`    // in the order they started
	Packages []*Result `json:"packages,omitempty"` // package outcomes, build failures among them
	Output   []string  `json:"output,omitempty"`   // lines that belong to no package
	Raw      string    `json:"raw,omitempty"`      // the runner's own output, as it printed it (raw JSON for most)
	Stderr   string    `json:"stderr,omitempty"`
	Coverage *Coverage `json:"coverage,omitempty"`

	index map[string]*Result
}

// NewRunID makes a run id.
func NewRunID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

func (r *Run) indexed() map[string]*Result {
	if r.index == nil {
		r.index = map[string]*Result{}
		for _, x := range r.Tests {
			r.index[x.Key()] = x
		}
		for _, x := range r.Packages {
			r.index[x.Key()] = x
		}
	}
	return r.index
}

func (r *Run) result(pkg, test string) *Result {
	r.indexed()
	k := Key(pkg, test)
	if x := r.index[k]; x != nil {
		return x
	}
	x := &Result{Package: pkg, Test: test, Status: StatusRunning}
	r.index[k] = x
	if test == "" {
		r.Packages = append(r.Packages, x)
	} else {
		r.Tests = append(r.Tests, x)
	}
	return x
}

// Apply folds an event into the run.
func (r *Run) Apply(e Event) {
	if r.Status == "" {
		r.Status = StatusRunning
	}
	if e.Kind == Output && e.Package == "" && e.Test == "" {
		r.Output = append(r.Output, e.Line)
		return
	}
	x := r.result(e.Package, e.Test)
	if e.Test == "" && e.Kind == Passed && x.Status.Failing() {
		return // a package reported in parts passes only if every part did
	}
	switch e.Kind {
	case Output:
		x.Output = append(x.Output, e.Line)
	case Passed:
		x.Status, x.Duration = StatusPassed, e.Elapsed
	case Skipped:
		x.Status, x.Duration, x.Message = StatusSkipped, e.Elapsed, e.Message
	case Failed:
		x.Status, x.Duration, x.Failure = StatusFailed, e.Elapsed, e.Failure
		if e.Failure != nil && e.Failure.Reason == ReasonTimeout {
			x.Status = StatusTimedOut
		}
	case BuildFailed:
		x.Status, x.Failure = StatusBuildFailed, e.Failure
	}
}

// Result is a test's result, by key.
func (r *Run) Result(key string) (*Result, bool) {
	x, ok := r.indexed()[key]
	return x, ok
}

// Failed is the tests that failed, in order.
func (r *Run) Failed() []*Result {
	var out []*Result
	for _, x := range r.Tests {
		if x.Status.Failing() {
			out = append(out, x)
		}
	}
	return out
}

// Slowest is the n slowest tests that finished, slowest first; every one when n <= 0.
func (r *Run) Slowest(n int) []*Result {
	var out []*Result
	for _, x := range r.Tests {
		if x.Status != StatusRunning {
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Duration > out[j].Duration })
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// Counts is how many tests passed, failed and were skipped.
func (r *Run) Counts() (passed, failed, skipped int) {
	for _, x := range r.Tests {
		switch {
		case x.Status == StatusPassed:
			passed++
		case x.Status.Failing():
			failed++
		case x.Status == StatusSkipped:
			skipped++
		}
	}
	return
}

// finishUnfinished fails the tests still running when the run was stopped.
func (r *Run) finishUnfinished(reason Reason, msg string, send func(Event)) {
	var open []*Result
	for _, x := range r.Tests {
		if x.Status == StatusRunning {
			open = append(open, x)
		}
	}
	for _, x := range open {
		send(Event{Kind: Failed, Package: x.Package, Test: x.Test, Failure: &Failure{Reason: reason, Message: msg}})
	}
}

// overall is a finished run's status, from its results and the runner's exit.
func (r *Run) overall(exit error) Status {
	failed := false
	for _, x := range append(append([]*Result(nil), r.Tests...), r.Packages...) {
		if x.Status.Failing() {
			failed = true
		}
	}
	var ee *exec.ExitError
	switch {
	case failed:
		return StatusFailed
	case exit != nil && !errors.As(exit, &ee):
		return StatusError
	case exit != nil:
		return StatusFailed
	case len(r.Tests) == 0 && len(r.Packages) == 0:
		return StatusSkipped
	}
	return StatusPassed
}
