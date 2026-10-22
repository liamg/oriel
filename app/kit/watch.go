package kit

import (
	"sync"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/registry"
)

// A burst of changes is one call: changed runs once they've stopped for watchSettle, or watchMost after the first.
const (
	watchSettle = 150 * time.Millisecond
	watchMost   = time.Second
)

// PathWatch calls changed, off the loop, when what it watches changes: told by the OS, or by polling every poll once
// the OS can't (out of watches, an unsupported platform, a watch that stopped working).
type PathWatch struct {
	polls   *PollSet
	poll    time.Duration
	changed func()
	w       *fswatch.Watcher
	failed  chan struct{} // closed once it polls instead
	kick    chan struct{}
	once    sync.Once
	done    chan struct{}
	wg      sync.WaitGroup
}

// WatchPaths watches with r's window's file watcher, polling every poll once it can't.
func WatchPaths(r registry.Registry, poll time.Duration, changed func()) *PathWatch {
	p := &PathWatch{polls: PollsOf(r), poll: poll, changed: changed, failed: make(chan struct{}), kick: make(chan struct{}, 1), done: make(chan struct{})}
	start := fswatch.New
	if w, ok := WindowOf(r); ok {
		start = w.FSWatch
	}
	if w, err := start(); err != nil {
		p.fail()
	} else {
		p.w = w
	}
	p.wg.Add(1)
	go p.run()
	return p
}

// Add watches a file, or a folder and its entries; it needn't exist yet.
func (p *PathWatch) Add(path string) {
	if p.w != nil && p.w.Add(path) != nil {
		p.fail()
	}
}

// AddTree watches a folder and everything under it but what skip leaves out (fswatch.Watcher.AddTree).
func (p *PathWatch) AddTree(root string, skip func(rel string) bool) {
	if p.w != nil && p.w.AddTree(root, skip) != nil {
		p.fail()
	}
}

// Kick calls changed as though something watched changed.
func (p *PathWatch) Kick() {
	select {
	case p.kick <- struct{}{}:
	default:
	}
}

// Polling says the OS couldn't watch it all, so it's polled.
func (p *PathWatch) Polling() bool {
	select {
	case <-p.failed:
		return true
	default:
		return false
	}
}

func (p *PathWatch) Close() {
	close(p.done)
	if p.w != nil {
		p.w.Close()
	}
	p.wg.Wait()
}

func (p *PathWatch) fail() { p.once.Do(func() { close(p.failed) }) }

func (p *PathWatch) run() {
	defer p.wg.Done()
	var events <-chan fswatch.Event
	if p.w != nil {
		events = p.w.Events()
	}
	var tick *Ticker
	var tc <-chan time.Time
	defer func() {
		if tick != nil {
			tick.Stop()
		}
	}()
	failed := p.failed
	var settle *time.Timer
	var sc <-chan time.Time
	var first time.Time
	for {
		select {
		case <-p.done:
			if settle != nil {
				settle.Stop()
			}
			return
		case <-failed:
			failed = nil
			tick = NewTicker(p.polls, p.poll)
			tc = tick.C
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if ev.Err != nil {
				p.fail()
			}
			settle, first = SettleBurst(settle, first)
			sc = settle.C
		case <-p.kick:
			settle, first = SettleBurst(settle, first)
			sc = settle.C
		case <-sc:
			settle, sc = nil, nil
			p.changed()
		case <-tc:
			p.changed()
		}
	}
}

// SettleBurst waits watchSettle more for a burst that began at first, or starts one.
func SettleBurst(t *time.Timer, first time.Time) (*time.Timer, time.Time) {
	now := time.Now()
	if t == nil {
		return time.NewTimer(watchSettle), now
	}
	t.Reset(min(watchSettle, max(0, first.Add(watchMost).Sub(now))))
	return t, first
}
