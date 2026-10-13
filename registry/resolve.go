package registry

import (
	"slices"
	"time"
)

// Resolver turns key presses into mappings, waiting for the rest of a sequence the way vim does
// (map.txt *map-typing*). It isn't safe for concurrent use; one runs per input loop.
type Resolver struct {
	reg      Registry
	modes    []string
	pending  []string
	cand     *Keymap // the longest complete mapping in pending so far
	candLen  int
	deadline time.Time
	since    time.Time // when the first pending key came
}

// Action is one thing to do: run Keymap, or pass Keys on unmapped.
type Action struct {
	Keymap *Keymap
	Keys   []string
}

func NewResolver(r Registry) *Resolver { return &Resolver{reg: r} }

// Feed takes one key, in notation, typed in modes (most specific first).
func (r *Resolver) Feed(modes []string, key string, now time.Time) []Action {
	r.modes = modes
	return r.feed(nil, []string{key}, now)
}

// Timeout gives up waiting once now reaches the deadline: the longest complete mapping runs, or else
// the first pending key passes on unmapped. Keys after either are fed again.
func (r *Resolver) Timeout(now time.Time) []Action {
	if len(r.pending) == 0 || r.deadline.IsZero() || now.Before(r.deadline) {
		return nil
	}
	out, rest := r.flush(nil, nil)
	return r.feed(out, rest, now)
}

// Deadline is when Timeout should be called, or zero when nothing is waiting on a timer.
func (r *Resolver) Deadline() time.Time { return r.deadline }

// Pending is the keys typed so far towards a longer mapping.
func (r *Resolver) Pending() []string { return slices.Clone(r.pending) }

// Since is when the first pending key came.
func (r *Resolver) Since() time.Time { return r.since }

// Modes are the modes the pending keys are looked up in.
func (r *Resolver) Modes() []string { return slices.Clone(r.modes) }

// Hold stops the pending keys timing out until the next key comes: which-key holds them while it shows them.
func (r *Resolver) Hold() { r.deadline = time.Time{} }

// Cancel drops the pending keys, as Esc in a half-typed mapping does: nothing runs.
func (r *Resolver) Cancel() {
	r.pending, r.cand, r.candLen, r.deadline = nil, nil, 0, time.Time{}
}

// BackUp drops the last pending key, as ⌫ in which-key does, and starts the wait again.
func (r *Resolver) BackUp(now time.Time) {
	if len(r.pending) == 0 {
		return
	}
	keys := r.pending[:len(r.pending)-1]
	since := r.since
	r.pending, r.cand, r.candLen = nil, nil, 0
	for i := range keys {
		if exact, _ := r.reg.Lookup(r.modes, keys[:i+1]); exact != nil {
			r.cand, r.candLen = exact, i+1
		}
	}
	r.pending = slices.Clone(keys)
	r.since = since
	r.arm(now)
}

func (r *Resolver) feed(out []Action, keys []string, now time.Time) []Action {
	for len(keys) > 0 {
		if len(r.pending) == 0 {
			r.since = now
		}
		r.pending = append(r.pending, keys[0])
		keys = keys[1:]
		exact, more := r.reg.Lookup(r.modes, r.pending)
		if exact != nil {
			r.cand, r.candLen = exact, len(r.pending)
		}
		if !more {
			out, keys = r.flush(out, keys)
		}
	}
	r.arm(now)
	return out
}

// arm sets the deadline for the keys pending, if they wait on one.
func (r *Resolver) arm(now time.Time) {
	r.deadline = time.Time{}
	if len(r.pending) > 0 {
		if on, _, _ := r.reg.Options().Get("keymap.timeout", At{}); on == true {
			d, _, _ := r.reg.Options().Get("keymap.timeoutlen", At{})
			r.deadline = now.Add(d.(time.Duration))
		}
	}
}

// flush settles pending and returns the keys still to feed.
func (r *Resolver) flush(out []Action, keys []string) ([]Action, []string) {
	var rest []string
	if r.cand != nil {
		out = append(out, Action{Keymap: r.cand})
		rest = r.pending[r.candLen:]
	} else {
		out = append(out, Action{Keys: r.pending[:1]})
		rest = r.pending[1:]
	}
	rest = append(slices.Clone(rest), keys...)
	r.pending, r.cand, r.candLen = nil, nil, 0
	return out, rest
}
