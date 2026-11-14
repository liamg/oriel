package registry

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"
)

// Hook kinds. A hook's result says which it did; Kind is what it was
// registered as, for :Hooks.
const (
	Observe = "observe"
	Veto    = "veto"
	Rewrite = "rewrite"
	Decide  = "decide"
)

// Verdict is what a hook says about an event: nothing (it goes on), a veto with a reason, or a decision. A rewrite
// changes the event it was given.
type Verdict struct {
	Veto    bool
	Reason  string
	Decided bool
	Value   any
	Rewrote bool // it changed the event, for :Hooks
}

// Hook runs on an event: every one of its name, or those whose subject (a file name) matches Pattern.
type Hook struct {
	Event    string // "BufWritePre", "Commit"
	Pattern  string // a glob over the subject, as "*.go"; "" for every one
	Priority int    // lower runs first
	Kind     string // Observe, Veto, Rewrite or Decide
	Label    string // what it does, for :Hooks: "allow go test"
	Fn       func(ctx Ctx, ev any) (Verdict, error)
	Source   string
}

// HookInfo is a registered hook and how long it takes, for :Hooks.
type HookInfo struct {
	Hook
	ID    string
	Calls int
	P99   time.Duration
	Off   error
	Did   string // what it last did: Observe, Veto, Rewrite or Decide, for a hook registered without a Kind
}

// EventInfo is an event hooks can be on: its name, what fires it, and its payload's fields for the Lua types.
type EventInfo struct {
	Name    string
	Doc     string
	Payload any  // a zero value of its struct
	Fired   bool // something fires it today; a hook on one that isn't waits for its feature
}

type hookEntry struct {
	h     Hook
	id    string
	did   string
	times []time.Duration // the last 256
	calls int
	over  bool // the last call went over the budget
}

type hooks struct {
	mu     sync.Mutex
	byID   map[string]*hookEntry
	order  []string
	events map[string]EventInfo
	next   int
}

// hookBudget is how long a hook may take before it's a warning: hooks.budget, kept off the hot path's allocations.
func (r *reg) hookBudget() time.Duration {
	if d := time.Duration(r.budget.Load()); d > 0 {
		return d
	}
	v, _, _ := r.Options().Get("hooks.budget", At{})
	d, _ := v.(time.Duration)
	d = cmp.Or(d, 2*time.Millisecond)
	r.budget.Store(int64(d))
	return d
}

func (r *reg) Hook(h Hook) Disposable {
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	if r.hooks.byID == nil {
		r.hooks.byID = map[string]*hookEntry{}
	}
	r.hooks.next++
	id := fmt.Sprintf("%s#%d", h.Event, r.hooks.next)
	r.hooks.byID[id] = &hookEntry{h: h, id: id}
	r.hooks.order = append(r.hooks.order, id)
	return DisposeFunc(func() {
		r.hooks.mu.Lock()
		defer r.hooks.mu.Unlock()
		delete(r.hooks.byID, id)
		r.hooks.order = slices.DeleteFunc(r.hooks.order, func(x string) bool { return x == id })
	})
}

func (r *reg) Hooks() []HookInfo {
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	var out []HookInfo
	for _, id := range r.hooks.order {
		e := r.hooks.byID[id]
		out = append(out, HookInfo{Hook: e.h, ID: id, Calls: e.calls, P99: p99(e.times), Off: r.faults.whyOff(KindHook, id), Did: e.did})
	}
	slices.SortStableFunc(out, func(a, b HookInfo) int {
		return cmp.Or(cmp.Compare(a.Event, b.Event), cmp.Compare(a.Priority, b.Priority))
	})
	return out
}

// DeclareEvent says what an event is, for :Hooks and the Lua types. fired is whether anything fires it yet.
func (r *reg) DeclareEvent(e EventInfo) Disposable {
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	if r.hooks.events == nil {
		r.hooks.events = map[string]EventInfo{}
	}
	r.hooks.events[e.Name] = e
	return DisposeFunc(func() {
		r.hooks.mu.Lock()
		defer r.hooks.mu.Unlock()
		delete(r.hooks.events, e.Name)
	})
}

func (r *reg) Events() []EventInfo {
	r.hooks.mu.Lock()
	defer r.hooks.mu.Unlock()
	var out []EventInfo
	for _, e := range r.hooks.events {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b EventInfo) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func p99(ts []time.Duration) time.Duration {
	if len(ts) == 0 {
		return 0
	}
	s := slices.Clone(ts)
	slices.Sort(s)
	return s[min(len(s)-1, len(s)*99/100)]
}

// Fire runs the hooks on event whose pattern matches subject, in priority order. A veto or a decision stops it and
// is returned; a rewrite changes ev as the hooks after it see it. A hook that panics is turned off, and one that
// errors is reported (FailLimit in a row turn it off); the errors are returned with the verdict, which the others
// still decide. A hook over its time budget on two calls running is a warning.
func (r *reg) Fire(ctx Ctx, event, subject string, ev any) (Verdict, []error) {
	r.provideEvent(event, subject)
	r.hooks.mu.Lock()
	var run []*hookEntry
	for _, id := range r.hooks.order {
		e := r.hooks.byID[id]
		if e.h.Event != event || e.h.Fn == nil || r.faults.whyOff(KindHook, id) != nil {
			continue
		}
		if !matchSubject(e.h.Pattern, subject) {
			continue
		}
		run = append(run, e)
	}
	r.hooks.mu.Unlock()
	slices.SortStableFunc(run, func(a, b *hookEntry) int { return cmp.Compare(a.h.Priority, b.h.Priority) })
	var errs []error
	for _, e := range run {
		var v Verdict
		start := r.clock.Now()
		err := Call(func() error {
			var err error
			v, err = e.h.Fn(ctx, ev)
			return err
		})
		took := r.clock.Now().Sub(start)
		r.hooks.mu.Lock()
		switch {
		case v.Veto:
			e.did = Veto
		case v.Decided:
			e.did = Decide
		case v.Rewrote:
			e.did = Rewrite
		case err == nil && e.did == "":
			e.did = Observe
		}
		e.calls++
		e.times = append(e.times, took)
		if len(e.times) > 256 {
			e.times = e.times[1:]
		}
		budget := r.hookBudget()
		over := err == nil && took > budget
		slow := over && e.over // twice running: a busy machine stalls any one call now and then
		e.over = over
		r.hooks.mu.Unlock()
		if p, ok := err.(*Panic); ok {
			r.faults.fault(Fault{Kind: KindHook, ID: e.id, Source: e.h.Source, Err: p})
		} else {
			r.Report(KindHook, e.id, err)
		}
		if slow {
			r.warn(KindHook, e.id, fmt.Errorf("%.1fms on %s · over the %v budget", float64(took.Microseconds())/1000, event, budget))
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s hook from %s: %w", event, e.h.Source, err))
			continue
		}
		if v.Veto || v.Decided {
			return v, errs
		}
	}
	return Verdict{}, errs
}

// On registers a hook on events of type E, named by the type: a typed hook a Go plugin writes.
func On[E any](r Registry, h Hook, fn func(ctx Ctx, ev *E) (Verdict, error)) Disposable {
	h.Event = EventName[E]()
	h.Fn = func(ctx Ctx, ev any) (Verdict, error) {
		e, ok := ev.(*E)
		if !ok {
			return Verdict{}, fmt.Errorf("%s hook given a %T", h.Event, ev)
		}
		return fn(ctx, e)
	}
	return r.Hook(h)
}

// EventName is an event type's name: its Go type name.
func EventName[E any]() string { return reflect.TypeFor[E]().Name() }
