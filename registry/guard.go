package registry

import (
	"fmt"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
)

// Kinds of contribution a fault can come from.
const (
	KindCommand    = "command"
	KindView       = "view"
	KindHook       = "hook"
	KindDecoration = "decoration"
)

// Panic is a contribution's panic, as an error.
type Panic struct {
	Value any
	Stack []byte
}

func (p *Panic) Error() string { return fmt.Sprintf("panic: %v", p.Value) }

// Call runs fn, turning a panic into a *Panic.
func Call(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &Panic{Value: v, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// Fault is a contribution that panicked or kept failing, and was turned off.
type Fault struct {
	Kind   string // KindCommand, KindView, KindHook
	ID     string
	Source string // who registered it
	Err    error  // a *Panic, or the last error
	Errors int    // the errors in a row that turned it off; 0 for a panic
}

// FailLimit is how many errors in a row turn a hook, view or decoration provider off.
const FailLimit = 3

// Failure is a contribution's error, or a warning about it: a hook over its time budget.
type Failure struct {
	Kind, ID, Source string
	Err              error
	InARow           int // its errors in a row, this one included; 0 for a warning
	Warn             bool
}

// Off is a contribution that's turned off, and why.
type Off struct {
	Kind, ID string
	Err      error
}

type offKey struct{ kind, id string }

// faults is what's turned off, and who hears about it.
type faults struct {
	mu     sync.Mutex
	off    map[offKey]error
	inARow map[offKey]int
	subs   []entry[func(Fault)]
	fsubs  []entry[func(Failure)]
}

// fault turns a contribution off and tells every subscriber.
func (f *faults) fault(ft Fault) {
	f.mu.Lock()
	if f.off == nil {
		f.off = map[offKey]error{}
	}
	f.off[offKey{ft.Kind, ft.ID}] = ft.Err
	var fns []func(Fault)
	for _, e := range f.subs {
		fns = append(fns, e.value)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(ft)
	}
}

// whyOff is why a contribution is turned off, or nil.
func (f *faults) whyOff(kind, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.off[offKey{kind, id}]
}

// OnFault calls fn each time a contribution is turned off. fn may run on any goroutine.
func (r *reg) OnFault(fn func(Fault)) Disposable {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	tok := r.tokens.Add(1)
	r.faults.subs = append(r.faults.subs, entry[func(Fault)]{tok, fn})
	return DisposeFunc(func() {
		r.faults.mu.Lock()
		defer r.faults.mu.Unlock()
		r.faults.subs = slices.DeleteFunc(r.faults.subs, func(e entry[func(Fault)]) bool { return e.token == tok })
	})
}

// OnFailure calls fn with each contribution's error and warning. fn may run on any goroutine.
func (r *reg) OnFailure(fn func(Failure)) Disposable {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	tok := r.tokens.Add(1)
	r.faults.fsubs = append(r.faults.fsubs, entry[func(Failure)]{tok, fn})
	return DisposeFunc(func() {
		r.faults.mu.Lock()
		defer r.faults.mu.Unlock()
		r.faults.fsubs = slices.DeleteFunc(r.faults.fsubs, func(e entry[func(Failure)]) bool { return e.token == tok })
	})
}

// Report says how a contribution's call went: nil for a success, which starts its count again, or its error.
// FailLimit errors in a row turn it off.
func (r *reg) Report(kind, id string, err error) {
	k := offKey{kind, id}
	r.faults.mu.Lock()
	if err == nil {
		delete(r.faults.inARow, k)
		r.faults.mu.Unlock()
		return
	}
	if r.faults.inARow == nil {
		r.faults.inARow = map[offKey]int{}
	}
	r.faults.inARow[k]++
	n := r.faults.inARow[k]
	if n >= FailLimit {
		delete(r.faults.inARow, k)
	}
	r.faults.mu.Unlock()
	src := r.sourceOf(kind, id)
	r.failed(Failure{Kind: kind, ID: id, Source: src, Err: err, InARow: n})
	if n >= FailLimit {
		r.faults.fault(Fault{Kind: kind, ID: id, Source: src, Err: err, Errors: n})
	}
}

// warn tells OnFailure's subscribers about a contribution that worked, but not as it should.
func (r *reg) warn(kind, id string, err error) {
	r.failed(Failure{Kind: kind, ID: id, Source: r.sourceOf(kind, id), Err: err, Warn: true})
}

func (r *reg) failed(f Failure) {
	r.faults.mu.Lock()
	var fns []func(Failure)
	for _, e := range r.faults.fsubs {
		fns = append(fns, e.value)
	}
	r.faults.mu.Unlock()
	for _, fn := range fns {
		fn(f)
	}
}

func (r *reg) TurnOff(kind, id string, err error) {
	r.faults.fault(Fault{Kind: kind, ID: id, Source: r.sourceOf(kind, id), Err: err})
}

func (r *reg) TurnOn(kind, id string) {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	delete(r.faults.off, offKey{kind, id})
	delete(r.faults.inARow, offKey{kind, id})
}

func (r *reg) WhyOff(kind, id string) error { return r.faults.whyOff(kind, id) }

func (r *reg) TurnedOff() []Off {
	r.faults.mu.Lock()
	defer r.faults.mu.Unlock()
	out := make([]Off, 0, len(r.faults.off))
	for k, err := range r.faults.off {
		out = append(out, Off{Kind: k.kind, ID: k.id, Err: err})
	}
	slices.SortFunc(out, func(a, b Off) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func (r *reg) sourceOf(kind, id string) string {
	switch kind {
	case KindCommand:
		r.mu.Lock()
		defer r.mu.Unlock()
		c, _ := r.commands.top(id)
		return c.Source
	case KindHook:
		r.hooks.mu.Lock()
		defer r.hooks.mu.Unlock()
		if e := r.hooks.byID[id]; e != nil {
			return e.h.Source
		}
		return ""
	}
	if c, ok := r.Contribution(kind, id); ok {
		return SourceOf(c)
	}
	return ""
}
