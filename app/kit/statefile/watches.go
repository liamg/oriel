package statefile

import (
	"context"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// ManagerFor is session id's freshness manager (services.Watches): the app's for "", and nil without one.
func ManagerFor(r registry.Registry, id core.SessionID) *watch.Manager {
	if w, ok := services.WatchesOf(r); ok {
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
	if w, ok := kit.WindowOf(r); ok {
		start = w.FSWatch
	}
	m = watch.New(watch.Options{Name: name, Watch: start, Timing: watch.Timing{Fallback: poll}, Polls: kit.Polls(r)})
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
	Pause()
	Resume()
}

// Subs are a plugin's subscriptions by key: Hold keeps one, KeepOnly closes those not named and PauseOnly pauses
// them.
type Subs struct {
	mu   sync.Mutex
	held map[string]heldSub
}

type heldSub struct {
	m      *watch.Manager
	sub    Closer
	need   watch.Need
	paused bool
}

// Hold keeps key subscribed in m with need: open makes the subscription the first time, or again when m has
// changed; after that only its need is changed, and it's resumed if paused.
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
		}
		resume := h.paused
		h.paused = false
		s.held[key] = h
		s.mu.Unlock()
		if resume {
			h.sub.Resume()
		}
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

// PauseOnly pauses every subscription not in keep: each keeps its reading, and reads nothing until held again.
func (s *Subs) PauseOnly(keep map[string]bool) {
	s.mu.Lock()
	var pause []Closer
	for k, h := range s.held {
		if !keep[k] && !h.paused {
			h.paused = true
			s.held[k] = h
			pause = append(pause, h.sub)
		}
	}
	s.mu.Unlock()
	for _, c := range pause {
		c.Pause()
	}
}

// Held is key's subscription, when it's held and a watch.Sub[T].
func Held[T any](s *Subs, key string) (watch.Sub[T], bool) {
	s.mu.Lock()
	h, ok := s.held[key]
	s.mu.Unlock()
	sub, typed := h.sub.(watch.Sub[T])
	return sub, ok && typed
}

// EachHeld calls fn with each subscription held that's a watch.Sub[T].
func EachHeld[T any](s *Subs, fn func(key string, sub watch.Sub[T])) {
	s.mu.Lock()
	subs := map[string]watch.Sub[T]{}
	for k, h := range s.held {
		if sub, ok := h.sub.(watch.Sub[T]); ok {
			subs[k] = sub
		}
	}
	s.mu.Unlock()
	for k, sub := range subs {
		fn(k, sub)
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
// intervals for a while, and Strain, when set, stretches them further as the API says its allowance runs low.
type Budget struct {
	PerHour int // requests an hour the polls may make
	// Strain is how many times slower to poll for what the API says is left of its allowance (registry.Strain); 1
	// while there's plenty. It's asked at most once a minute.
	Strain func() float64

	mu       sync.Mutex
	users    map[string]int // requests a read makes, by source
	backed   time.Time      // intervals are doubled until then
	strain   float64        // Strain's last answer
	strained time.Time      // when it was asked
}

// strainNow is Strain's last answer, asked again (without b.mu held: a plugin may take a while) once it's a minute
// old; meanwhile the last answer stands.
func (b *Budget) strainNow() float64 {
	now := time.Now()
	b.mu.Lock()
	f, last := b.Strain, max(b.strain, 1)
	if f == nil || !b.strained.IsZero() && now.Sub(b.strained) < time.Minute {
		b.mu.Unlock()
		return last
	}
	b.strained = now
	b.mu.Unlock()
	s := max(f(), 1)
	b.mu.Lock()
	b.strain = s
	b.mu.Unlock()
	return s
}

// Paced is how often a poll on screen, wanting maxAge, runs: as asked, stretched only by the rate limit's strain.
func (b *Budget) Paced(maxAge time.Duration) time.Duration {
	strain := b.strainNow()
	b.mu.Lock()
	defer b.mu.Unlock()
	d := time.Duration(float64(maxAge) * strain)
	if time.Now().Before(b.backed) {
		d *= 2
	}
	return d
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
	strain := b.strainNow()
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
	d = time.Duration(float64(d) * strain)
	if time.Now().Before(b.backed) {
		d *= 2
	}
	return d
}

// BackOff says the API refused for its rate limit: intervals double for the next quarter hour.
func (b *Budget) BackOff() {
	b.mu.Lock()
	b.backed = time.Now().Add(15 * time.Minute)
	b.mu.Unlock()
}

// CodeHostStrain is registry.Strain over every code host's allowance, each given a few seconds to say.
func CodeHostStrain(ctx context.Context, r registry.Contributions) float64 {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return registry.Strain(registry.Allowances(ctx, r), time.Now())
}
