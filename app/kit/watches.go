package kit

import (
	"context"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// ManagerFor is session id's freshness manager (registry.Watches): the app's for "", and nil without one.
func ManagerFor(r registry.Registry, id core.SessionID) *watch.Manager {
	if w, ok := registry.WatchesOf(r); ok {
		return w.For(id)
	}
	return nil
}

// FileManager is r's app manager or, with none to share, one of its own named name, which watches files through the
// OS (the window's, when there is one) and polls every poll when it can't (0: the manager's default). done closes
// the one of its own.
func FileManager(r registry.Registry, name string, poll time.Duration) (m *watch.Manager, done func()) {
	if m = ManagerFor(r, ""); m != nil {
		return m, func() {}
	}
	start := fswatch.New
	if w, ok := WindowOf(r); ok {
		start = w.FSWatch
	}
	m = watch.New(watch.Options{Name: name, Watch: start, Timing: watch.Timing{Fallback: poll}, Polls: PollsOf(r)})
	return m, m.Close
}

// Every is a source whose read runs fn, polled at its subscriptions' MaxAge: periodic work a manager runs while
// something subscribes, and stops when nothing does. Its reading is when fn last finished. interval stretches the
// poll (a budget shared with other sources); nil leaves it.
func Every(key string, fn func(ctx context.Context) error, interval func(maxAge time.Duration) time.Duration) watch.Source[time.Time] {
	return watch.Source[time.Time]{Key: key, Poll: true, Interval: interval, Read: func(r watch.Reader) (time.Time, error) {
		err := fn(r.Context())
		return time.Now(), err
	}}
}

// Closer is a subscription as Subs holds it: any watch.Sub.
type Closer interface {
	Close()
	SetNeed(watch.Need)
	Valid() bool
	Refresh()
	Busy() bool
}

// Subs are a plugin's subscriptions by key: Hold keeps one, Only closes those not named.
type Subs struct {
	mu   sync.Mutex
	held map[string]heldSub
}

type heldSub struct {
	m    *watch.Manager
	sub  Closer
	need watch.Need
}

// Hold keeps key subscribed in m with need: open makes the subscription the first time, or again when m has
// changed; after that only its need is changed.
func (s *Subs) Hold(key string, m *watch.Manager, need watch.Need, open func() Closer) {
	if m == nil {
		s.Drop(key)
		return
	}
	s.mu.Lock()
	if s.held == nil {
		s.held = map[string]heldSub{}
	}
	h, ok := s.held[key]
	if ok && h.m == m {
		if h.need != need {
			h.sub.SetNeed(need)
			h.need = need
			s.held[key] = h
		}
		s.mu.Unlock()
		return
	}
	delete(s.held, key)
	s.mu.Unlock()
	if ok {
		h.sub.Close()
	}
	sub := open()
	s.mu.Lock()
	s.held[key] = heldSub{m: m, sub: sub, need: need}
	s.mu.Unlock()
}

// Refresh reads key's source again now (watch.Sub.Refresh).
func (s *Subs) Refresh(key string) {
	s.mu.Lock()
	h, ok := s.held[key]
	s.mu.Unlock()
	if ok {
		h.sub.Refresh()
	}
}

// Drop closes key's subscription.
func (s *Subs) Drop(key string) {
	s.mu.Lock()
	h, ok := s.held[key]
	delete(s.held, key)
	s.mu.Unlock()
	if ok {
		h.sub.Close()
	}
}

// KeepOnly closes every subscription not in keep.
func (s *Subs) KeepOnly(keep map[string]bool) {
	s.mu.Lock()
	var gone []Closer
	for k, h := range s.held {
		if !keep[k] {
			gone = append(gone, h.sub)
			delete(s.held, k)
		}
	}
	s.mu.Unlock()
	for _, c := range gone {
		c.Close()
	}
}

// Busy says a held subscription's source is being read, or is about to be.
func (s *Subs) Busy() bool {
	s.mu.Lock()
	subs := make([]Closer, 0, len(s.held))
	for _, h := range s.held {
		subs = append(subs, h.sub)
	}
	s.mu.Unlock()
	for _, c := range subs {
		if c.Busy() {
			return true
		}
	}
	return false
}

// Len is how many subscriptions are held.
func (s *Subs) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.held)
}

// Close closes them all.
func (s *Subs) Close() { s.KeepOnly(nil) }

// Budget shares an API's request allowance between the sources polling it: each says how many requests a read
// makes, and Interval stretches every poll so that together they keep to PerHour. A rate limit hit doubles the
// intervals for a while.
type Budget struct {
	PerHour int // requests an hour the polls may make

	mu     sync.Mutex
	users  map[string]int // requests a read makes, by source
	backed time.Time      // intervals are doubled until then
}

// Use counts key's reads, of perRead requests each, until the release.
func (b *Budget) Use(key string, perRead int) (release func()) {
	b.mu.Lock()
	if b.users == nil {
		b.users = map[string]int{}
	}
	b.users[key] = perRead
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.users, key)
		b.mu.Unlock()
	}
}

// Interval is how often a poll wanting maxAge runs: no sooner than the allowance lets every user poll in turn.
func (b *Budget) Interval(maxAge time.Duration) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, r := range b.users {
		n += r
	}
	d := maxAge
	if b.PerHour > 0 && n > 0 {
		d = max(d, time.Duration(n)*time.Hour/time.Duration(b.PerHour))
	}
	if time.Now().Before(b.backed) {
		d *= 2
	}
	return d
}

// Limited says the API refused for its rate limit: intervals double for the next quarter hour.
func (b *Budget) Limited() {
	b.mu.Lock()
	b.backed = time.Now().Add(15 * time.Minute)
	b.mu.Unlock()
}

// LoadState is a load as the toolkit shows it.
func LoadState(l registry.Load) widget.LoadState {
	switch {
	case l.Done:
		return widget.LoadState{Phase: widget.Ready}
	case l.Err != nil:
		return widget.LoadState{Phase: widget.Failed, Err: l.Err}
	}
	return widget.LoadState{Phase: widget.Pending, Since: l.Since}
}
