// Package watch keeps readings of things up to date for whoever subscribes to them, and only while someone does.
//
// A Source is something to read: files, a work tree's git status, a pull request. It says how it goes stale: its
// files change, a source it's read from has a new reading, or it gets old (a poll). A Manager holds one shared
// reading per source and reads it again when it's stale and some subscription needs it fresh; with no subscribers it
// watches and polls nothing. A subscription's Need says how stale its reading may be, and what it wants while the
// window is behind another app: nothing (Pause, the default), readings less often (Slow), or the same (Keep).
package watch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/internal/clock"
)

// Background is what a subscription wants while the window is behind another app.
type Background int

const (
	Pause Background = iota // no reads; one catch-up read when the window comes back, if it went stale
	Slow                    // reads at Need.Slow
	Keep                    // reads as in front
)

func (b Background) String() string { return [...]string{"pause", "slow", "keep"}[b] }

// Need is how fresh a subscription wants its reading.
type Need struct {
	// MaxAge is how stale a reading may be: a polled source is read this often, a watched one at most this often
	// once it changes. 0 reads a watched source as soon as its changes settle.
	MaxAge     time.Duration
	Background Background
	Slow       time.Duration // MaxAge in the background for Slow; 10×MaxAge, at least a minute, when 0
}

func (n Need) background() time.Duration {
	if n.Slow > 0 {
		return n.Slow
	}
	return max(10*n.MaxAge, time.Minute)
}

// Files is a path a source is read from: its changes make the source stale.
type Files struct {
	Path string
	Tree bool                  // the folder and everything under it
	Skip func(rel string) bool // for a tree: what to leave out, by slash path relative to Path
}

// Reader is what a source's read is given.
type Reader interface {
	Context() context.Context
	// Changed are the paths of its Files that changed since its last read began; nil for the first read, or a poll.
	Changed() []string
	dep(key string) (any, bool)
}

// Dep is the reading of a source s's spec lists in Deps, inside its read.
func Dep[T any](r Reader, s Source[T]) (T, bool) {
	v, ok := r.dep(s.Key)
	t, _ := v.(T)
	return t, ok
}

// Source is something to read, and how it goes stale.
type Source[T any] struct {
	Key   string
	Files []Files           // watched: a change makes it stale
	Deps  []AnySource       // read from: a new reading of one makes it stale
	Poll  bool              // stale once older than its subscriptions' MaxAge
	Same  func(a, b T) bool // a reading the same as the last isn't a change; every reading is one when nil
	// Interval stretches a poll's MaxAge: a budget shared between sources, say.
	Interval func(maxAge time.Duration) time.Duration
	Read     func(r Reader) (T, error)
}

// AnySource is a Source of any type, for Deps.
type AnySource interface{ spec() spec }

type spec struct {
	key      string
	files    []Files
	deps     []spec
	poll     bool
	same     func(a, b any) bool
	interval func(time.Duration) time.Duration
	read     func(r Reader) (any, error)
}

func (s Source[T]) spec() spec {
	var deps []spec
	for _, d := range s.Deps {
		deps = append(deps, d.spec())
	}
	sp := spec{key: s.Key, files: s.Files, deps: deps, poll: s.Poll, interval: s.Interval,
		read: func(r Reader) (any, error) { return s.Read(r) }}
	if s.Same != nil {
		sp.same = func(a, b any) bool {
			x, _ := a.(T)
			y, _ := b.(T)
			return s.Same(x, y)
		}
	}
	return sp
}

// Timing a Manager reads by; zero fields take the defaults.
type Timing struct {
	Settle   time.Duration // how long a burst of changes is let settle: 150ms
	Most     time.Duration // the longest a burst holds a read back: 1s
	Grace    time.Duration // how long a source no one subscribes to keeps its watches and reading: 30s
	Fallback time.Duration // how often a source whose files can't be watched is polled: 2s
}

func (t Timing) or() Timing {
	if t.Settle == 0 {
		t.Settle = 150 * time.Millisecond
	}
	if t.Most == 0 {
		t.Most = time.Second
	}
	if t.Grace == 0 {
		t.Grace = 30 * time.Second
	}
	if t.Fallback == 0 {
		t.Fallback = 2 * time.Second
	}
	return t
}

// Options make a Manager.
type Options struct {
	Name   string                           // what it's for: a session's id, or "app"
	Watch  func() (*fswatch.Watcher, error) // the file watcher; fswatch.New when nil
	Timing Timing
	Log    func(msg string, args ...any) // read failures and panics
	Polls  Polls                         // told of each source polled, for the idle budget
	Clock  clock.Clock                   // what it times settling, reads and grace by; the system's when nil
}

// Polls note periodic work while it runs.
type Polls interface {
	Start(every time.Duration) (stop func())
}

// Manager keeps the readings its subscriptions need, and no others.
type Manager struct {
	opts   Options
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	readEnd sync.Cond // a read finished; its L is mu
	nodes   map[string]*node
	focused bool
	closed  bool
	fw      *fswatch.Watcher
	fwErr   error
	roots   fsRoots
}

// New starts a manager, with the window in front.
func New(o Options) *Manager {
	o.Timing = o.Timing.or()
	o.Clock = clock.Or(o.Clock)
	if o.Watch == nil {
		o.Watch = fswatch.New
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{opts: o, ctx: ctx, cancel: cancel, nodes: map[string]*node{}, focused: true,
		roots: fsRoots{files: map[string][]Files{}}}
	m.readEnd.L = &m.mu
	return m
}

func (m *Manager) Name() string { return m.opts.Name }

// SetFocused says whether the window is in front: behind another app, Pause subscriptions stop reading, and Slow
// ones read less often. Coming back, what went stale is read once.
func (m *Manager) SetFocused(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.focused == on || m.closed {
		return
	}
	m.focused = on
	for _, n := range m.nodes {
		n.update()
	}
}

// Idle waits until no read is under way. What a finished read sets off on the manager's clock (a settle, a source
// reading from it) is not under way until that clock reaches it.
func (m *Manager) Idle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for m.reading() {
		m.readEnd.Wait()
	}
}

// reading says a read is under way; m.mu is held.
func (m *Manager) reading() bool {
	for _, n := range m.nodes {
		if n.reading {
			return true
		}
	}
	return false
}

// Close stops every watch, poll and read, and ends every subscription's readings.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	for _, n := range m.nodes {
		n.stop()
	}
	m.nodes = map[string]*node{}
	fw := m.fw
	m.fw = nil
	m.mu.Unlock()
	m.cancel()
	if fw != nil {
		fw.Close()
	}
	m.wg.Wait()
}

// Sub is a subscription to a source's readings.
type Sub[T any] struct{ s *sub }

// Subscribe asks for src's readings as fresh as need, until Close. label names who asked, for the Performance page.
func Subscribe[T any](m *Manager, src Source[T], need Need, label string) Sub[T] {
	return Sub[T]{m.subscribe(src.spec(), need, label, false, nil)}
}

// SubscribeFunc is Subscribe with onChange called after each new reading from the first on (Sub.OnChange), set
// before any can arrive.
func SubscribeFunc[T any](m *Manager, src Source[T], need Need, label string, onChange func()) Sub[T] {
	return Sub[T]{m.subscribe(src.spec(), need, label, false, onChange)}
}

// Get is the latest reading, when it was read, and whether there is one that didn't fail.
func (s Sub[T]) Get() (T, time.Time, bool) {
	v, at, err, ok := s.s.reading()
	t, _ := v.(T)
	return t, at, ok && err == nil
}

// Err is why the latest read failed; nil when it didn't.
func (s Sub[T]) Err() error {
	_, _, err, _ := s.s.reading()
	return err
}

// Fresh reads the source again now, as Refresh does, and waits for that reading, or why it failed: a reading begun
// after the call, never one kept from before (a file written just now). ctx gives up.
func (s Sub[T]) Fresh(ctx context.Context) (T, error) {
	since := s.s.n.m.opts.Clock.Now()
	s.Refresh()
	tick := time.NewTicker(10 * time.Millisecond) // Changed may be drained by another reader of it
	defer tick.Stop()
	for {
		if v, err, ok := s.s.since(since); ok {
			t, _ := v.(T)
			return t, err
		}
		select {
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		case <-s.s.changed:
		case <-tick.C:
		}
	}
}

// Changed fires after each new reading.
func (s Sub[T]) Changed() <-chan struct{} { return s.s.changed }

// OnChange calls fn after each new reading, off the manager's lock; it replaces any fn before.
func (s Sub[T]) OnChange(fn func()) { s.s.setOnChange(fn) }

// SetNeed changes how fresh the reading is wanted.
func (s Sub[T]) SetNeed(n Need) { s.s.setNeed(n) }

// Pause stops wanting readings until Resume; the last one is kept.
func (s Sub[T]) Pause()  { s.s.setPaused(true) }
func (s Sub[T]) Resume() { s.s.setPaused(false) }

// Close ends the subscription.
func (s Sub[T]) Close() { s.s.close() }

// Refresh reads the source again, and what it's read from, as though they'd all changed: after work the watches
// may not see, or a Retry.
func (s Sub[T]) Refresh() {
	m := s.s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.s.closed {
		s.s.n.refresh()
	}
}

// Busy says the source, or one it's read from, is being read or is stale and about to be.
func (s Sub[T]) Busy() bool {
	m := s.s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	return !s.s.closed && s.s.n.busy()
}

// Valid is a subscription, not the zero Sub.
func (s Sub[T]) Valid() bool { return s.s != nil }

type sub struct {
	n        *node
	need     Need
	label    string
	paused   bool
	internal bool // a source's hold on one it reads from
	closed   bool
	changed  chan struct{}
	onChange func()
}

func (m *Manager) subscribe(sp spec, need Need, label string, internal bool, onChange func()) *sub {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := &sub{need: need, label: label, internal: internal, changed: make(chan struct{}, 1), onChange: onChange}
	if m.closed {
		s.closed = true
		s.n = &node{m: m, spec: sp, subs: map[*sub]bool{}}
		return s
	}
	n := m.nodes[sp.key]
	if n == nil {
		n = &node{m: m, spec: sp, subs: map[*sub]bool{}}
		m.nodes[sp.key] = n
	}
	s.n = n
	n.subs[s] = true
	n.update()
	if n.has {
		s.notify() // a reading already there is news to it
	}
	return s
}

func (s *sub) reading() (any, time.Time, error, bool) {
	m := s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	return s.n.value, s.n.at, s.n.err, s.n.has
}

// Since is the latest reading, and whether there's one whose read began at t or after.
func (s Sub[T]) Since(t time.Time) (T, error, bool) {
	v, err, ok := s.s.since(t)
	r, _ := v.(T)
	return r, err, ok
}

// since is the latest reading, if its read began at t or after.
func (s *sub) since(t time.Time) (any, error, bool) {
	m := s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.n.has || s.n.from.Before(t) {
		return nil, nil, false
	}
	return s.n.value, s.n.err, true
}

func (s *sub) setOnChange(fn func()) {
	s.n.m.mu.Lock()
	s.onChange = fn
	s.n.m.mu.Unlock()
}

func (s *sub) setNeed(need Need) {
	m := s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.need == need || s.closed {
		return
	}
	s.need = need
	s.n.update()
}

func (s *sub) setPaused(p bool) {
	m := s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.paused == p || s.closed {
		return
	}
	s.paused = p
	s.n.update()
}

func (s *sub) close() {
	m := s.n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	delete(s.n.subs, s)
	s.n.update()
}

// notify tells s there's a new reading; m.mu is held, and fn runs after it's let go.
func (s *sub) notify() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
	if fn := s.onChange; fn != nil {
		s.n.m.wg.Add(1)
		go func() {
			defer s.n.m.wg.Done()
			fn()
		}()
	}
}

// node is a source's shared reading, and what keeps it fresh; its fields are guarded by m.mu.
type node struct {
	m    *Manager
	spec spec
	subs map[*sub]bool

	value any
	at    time.Time
	from  time.Time // when the read behind the latest reading began
	err   error
	has   bool
	reads int
	times []time.Time // when the last minute's reads began

	active    bool // some subscription wants it read
	held      bool // watched and holding its deps: active, or within grace
	paused    bool // subscribed, but no one wants reads now
	need      Need // the tightest active need
	every     time.Duration
	deps      []*sub    // its holds on the sources it reads from
	watching  []string  // its paths registered with the file watcher
	fallback  bool      // its files couldn't be watched: polled at Timing.Fallback
	dirty     bool      // stale since its last read began
	urgent    bool      // Refresh asked for a read now
	first     time.Time // when it went stale, for a burst
	last      time.Time // the latest change
	changed   []string  // paths changed since its last read began
	cause     string    // what last made it stale: a path, a source it reads from, or "refresh"
	readCause string    // the cause of its last read; "poll" for a poll
	reading   bool      // a read is running
	lastStart time.Time // when the last read began
	timer     clock.Timer
	idle      time.Time     // when it lost its last subscription
	polling   time.Duration // the period noted with Options.Polls
	unnote    func()
}

// update works out what n's subscriptions want now, and sets watches, holds and the next read to match.
func (n *node) update() {
	m := n.m
	if m.closed {
		return
	}
	var need Need
	active, subscribed := false, false
	for s := range n.subs {
		if s.closed {
			continue
		}
		subscribed = true
		if s.paused {
			continue
		}
		age := s.need.MaxAge
		if !m.focused {
			switch s.need.Background {
			case Pause:
				continue
			case Slow:
				age = s.need.background()
			}
		}
		if !active || age < need.MaxAge {
			need.MaxAge = age
		}
		active = true
	}
	need.Background = Keep // what it holds its deps with: it decides for them
	n.active, n.paused, n.need = active, subscribed && !active, need
	switch {
	case subscribed:
		n.idle = time.Time{}
		n.hold()
	case n.held && n.idle.IsZero():
		n.idle = n.m.opts.Clock.Now()
	}
	for _, d := range n.deps {
		d.need = need
		d.paused = !active
		d.n.update()
	}
	n.schedule()
}

// hold watches n's files and subscribes to its deps; m.mu is held.
func (n *node) hold() {
	if n.held {
		return
	}
	n.held = true
	if len(n.spec.files) > 0 && !n.m.watch(n) {
		n.fallback = true
	}
	for _, d := range n.spec.deps {
		s := &sub{need: n.need, label: n.spec.key, internal: true, paused: !n.active, changed: make(chan struct{}, 1)}
		dn := n.m.nodes[d.key]
		if dn == nil {
			dn = &node{m: n.m, spec: d, subs: map[*sub]bool{}}
			n.m.nodes[d.key] = dn
		}
		s.n = dn
		dn.subs[s] = true
		n.deps = append(n.deps, s)
		dn.update()
	}
	if !n.has {
		n.dirty = true
	}
}

// release stops n's watches and holds, once its grace is over; m.mu is held.
func (n *node) release() {
	n.held, n.fallback = false, false
	n.m.unwatch(n)
	for _, d := range n.deps {
		d.closed = true
		delete(d.n.subs, d)
		d.n.update()
	}
	n.deps = nil
}

func (n *node) stop() {
	if n.timer != nil {
		n.timer.Stop()
	}
	if n.unnote != nil {
		n.unnote()
		n.unnote = nil
	}
	for s := range n.subs {
		s.closed = true
	}
}

// note tells Options.Polls of n's poll, or that it stopped; m.mu is held.
func (n *node) note() {
	var every time.Duration
	if n.active && n.held && !n.m.closed {
		switch {
		case n.spec.poll:
			every = n.pollEvery()
		case n.fallback:
			every = n.m.opts.Timing.Fallback
		}
	}
	if every == n.polling {
		return
	}
	if n.unnote != nil {
		n.unnote()
		n.unnote = nil
	}
	n.polling = every
	if every > 0 && n.m.opts.Polls != nil {
		n.unnote = n.m.opts.Polls.Start(every)
	}
}

// schedule sets n's timer for its next read, or its release; m.mu is held.
func (n *node) schedule() {
	n.note()
	if n.timer != nil {
		n.timer.Stop()
		n.timer = nil
	}
	now := n.m.opts.Clock.Now()
	t := n.m.opts.Timing
	var at time.Time
	switch {
	case !n.held:
		return
	case !n.active && !n.idle.IsZero():
		at = n.idle.Add(t.Grace) // to let go
	case !n.active || n.reading || n.waiting():
		return
	case n.dirty && (!n.has || n.urgent):
		at = now
	case n.dirty:
		at = minTime(n.last.Add(t.Settle), n.first.Add(max(t.Most, n.need.MaxAge)))
		if n.need.MaxAge > 0 {
			at = maxTime(at, n.lastStart.Add(n.need.MaxAge))
		}
	case n.spec.poll:
		at = n.at.Add(n.pollEvery())
	case n.fallback:
		at = n.lastStart.Add(t.Fallback)
	default:
		return
	}
	n.timer = n.m.opts.Clock.AfterFunc(max(0, at.Sub(now)), n.fire)
}

// waiting says a source n reads from is being read, or is about to be: n reads after it.
func (n *node) waiting() bool {
	for _, d := range n.deps {
		if d.n.reading || d.n.dirty || !d.n.has {
			return true
		}
	}
	return false
}

func (n *node) pollEvery() time.Duration {
	if n.spec.interval != nil {
		return n.spec.interval(n.need.MaxAge)
	}
	return n.need.MaxAge
}

// fire is n's timer: a read, or letting go after its grace.
func (n *node) fire() {
	m := n.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.nodes[n.spec.key] != n {
		return
	}
	now := m.opts.Clock.Now()
	grace := m.opts.Timing.Grace
	if !n.active && !n.idle.IsZero() && now.Sub(n.idle) >= grace {
		if n.held {
			n.release()
		}
		if len(n.subs) == 0 {
			delete(m.nodes, n.spec.key) // its reading is let go with it
			return
		}
		n.schedule()
		return
	}
	if !n.active || n.reading {
		n.schedule()
		return
	}
	if !n.dirty && !n.spec.poll && !n.fallback {
		return
	}
	n.read()
}

// read starts a read of n off the lock; m.mu is held.
func (n *node) read() {
	m := n.m
	n.readCause = n.cause
	if !n.dirty {
		n.readCause = "poll"
	}
	n.reading, n.dirty, n.urgent, n.cause = true, false, false, ""
	n.lastStart = m.opts.Clock.Now()
	n.reads++
	n.times = append(n.times, n.lastStart)
	if cut := n.lastStart.Add(-time.Minute); len(n.times) > 0 && n.times[0].Before(cut) {
		i, _ := slices.BinarySearchFunc(n.times, cut, func(a, b time.Time) int { return a.Compare(b) })
		n.times = n.times[i:]
	}
	r := &reader{ctx: m.ctx, changed: n.changed, deps: map[string]any{}}
	n.changed = nil
	var depErr error
	for _, d := range n.deps {
		if d.n.has && d.n.err == nil {
			r.deps[d.n.spec.key] = d.n.value
		} else if d.n.err != nil && depErr == nil {
			depErr = d.n.err
		}
	}
	read, began := n.spec.read, n.lastStart
	if depErr != nil {
		read = func(Reader) (any, error) { return nil, depErr } // a source read from one that failed fails with it
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		v, err := safeRead(read, r)
		m.mu.Lock()
		defer m.mu.Unlock()
		n.reading = false
		m.readEnd.Broadcast()
		if m.closed || m.nodes[n.spec.key] != n {
			return
		}
		if err != nil && m.ctx.Err() != nil {
			return
		}
		if err != nil && m.opts.Log != nil {
			m.opts.Log("watch: read failed", "manager", m.opts.Name, "source", n.spec.key, "err", err)
		}
		news := !n.has || err != nil || n.err != nil || n.spec.same == nil || !n.spec.same(n.value, v)
		if err == nil || !n.has {
			n.value = v
		}
		n.err, n.at, n.from, n.has = err, m.opts.Clock.Now(), began, true
		if news {
			for s := range n.subs {
				if s.internal {
					s.n.dirtyFrom(s.label)
				}
			}
			for s := range n.subs {
				if !s.closed {
					s.notify()
				}
			}
		}
		n.wakeDependents()
		n.schedule()
	}()
}

// dirtyFrom marks the source keyed key stale: a source it reads from has a new reading.
func (n *node) dirtyFrom(key string) {
	d := n.m.nodes[key]
	if d == nil {
		return
	}
	d.stale(nil, n.spec.key)
}

// wakeDependents lets the sources that read n, and were waiting for it, read.
func (n *node) wakeDependents() {
	for s := range n.subs {
		if s.internal {
			if d := n.m.nodes[s.label]; d != nil {
				d.schedule()
			}
		}
	}
}

// refresh marks n and what it reads from stale, to be read without waiting for changes to settle; m.mu is held.
func (n *node) refresh() {
	for _, d := range n.deps {
		d.n.refresh()
	}
	n.urgent = true
	n.stale(nil, "refresh")
}

// busy says n, or what it reads from, is being read or is due a read; m.mu is held.
func (n *node) busy() bool {
	if n.reading || n.active && (n.dirty || !n.has) {
		return true
	}
	for _, d := range n.deps {
		if d.n.busy() {
			return true
		}
	}
	return false
}

// stale marks n stale, with the paths that changed and why; m.mu is held.
func (n *node) stale(paths []string, cause string) {
	now := n.m.opts.Clock.Now()
	if !n.dirty {
		n.dirty, n.first = true, now
	}
	n.last, n.cause = now, cause
	n.changed = append(n.changed, paths...)
	if len(n.changed) > 4096 {
		n.changed = n.changed[len(n.changed)-4096:]
	}
	n.schedule()
}

func safeRead(read func(Reader) (any, error), r Reader) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("watch: read panicked: %v", p)
		}
	}()
	return read(r)
}

type reader struct {
	ctx     context.Context
	changed []string
	deps    map[string]any
}

func (r *reader) Context() context.Context { return r.ctx }
func (r *reader) Changed() []string        { return r.changed }
func (r *reader) dep(key string) (any, bool) {
	v, ok := r.deps[key]
	return v, ok
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// FilesSource is files' changes: each reading is the paths that changed since the last, nil the first time.
func FilesSource(key string, files ...Files) Source[[]string] {
	return Source[[]string]{Key: key, Files: files, Read: func(r Reader) ([]string, error) {
		return slices.Compact(slices.Sorted(slices.Values(r.Changed()))), nil
	}}
}

// File is the contents of the file at path, read again when it changes: nil while there's no file. A reading the
// same as the last isn't a change.
func File(path string) Source[[]byte] {
	return Source[[]byte]{Key: "file:" + path, Files: []Files{{Path: path}}, Same: bytes.Equal, Read: func(Reader) ([]byte, error) {
		b, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return b, err
	}}
}

// Derive is a source read from src: fn turns each of its readings into this one's, with no work of its own to keep
// fresh.
func Derive[T, U any](key string, src Source[T], fn func(T) (U, error)) Source[U] {
	return Source[U]{Key: key, Deps: []AnySource{src}, Read: func(r Reader) (U, error) {
		v, _ := Dep(r, src)
		return fn(v)
	}}
}
