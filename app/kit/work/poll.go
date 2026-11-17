package work

import (
	"sync"
	"time"
)

// UnfocusedPoll is how often network polls run while the window hasn't focus: a watch.Need's Slow.
const UnfocusedPoll = 5 * time.Minute

// PollSet is the periods of a window's periodic work while it runs (Polls). A nil one notes nothing.
type PollSet struct {
	mu      sync.Mutex
	live    map[int]time.Duration
	next    int
	fastest time.Duration // since the last reset
}

// Start notes work running every d until stop.
func (p *PollSet) Start(d time.Duration) (stop func()) {
	if p == nil {
		return func() {}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.live == nil {
		p.live = map[int]time.Duration{}
	}
	id := p.next
	p.next++
	p.live[id] = d
	if p.fastest == 0 || d < p.fastest {
		p.fastest = d
	}
	return func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		delete(p.live, id)
	}
}

// Reset forgets all but what's running now.
func (p *PollSet) Reset() {
	if p == nil {
		return
	}
	f := p.FastestNow()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.fastest = f
}

// FastestSince is the shortest period running since the last reset, or 0.
func (p *PollSet) FastestSince() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fastest
}

// Ticker is a time.Ticker noted in polls.
type Ticker struct {
	*time.Ticker
	stop func()
}

// NewTicker is a ticker every d, noted in polls.
func NewTicker(polls *PollSet, d time.Duration) *Ticker {
	return &Ticker{time.NewTicker(d), polls.Start(d)}
}

func (t *Ticker) Stop() {
	t.Ticker.Stop()
	t.stop()
}

// FastestNow is the shortest period running now, or 0.
func (p *PollSet) FastestNow() time.Duration {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var f time.Duration
	for _, d := range p.live {
		if f == 0 || d < f {
			f = d
		}
	}
	return f
}

// Signals are funcs called together each time something happens.
type Signals struct {
	mu   sync.Mutex
	fns  map[int]func()
	next int
}

// On calls fn each time, until cancelled.
func (s *Signals) On(fn func()) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fns == nil {
		s.fns = map[int]func(){}
	}
	id := s.next
	s.next++
	s.fns[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.fns, id)
	}
}

func (s *Signals) Fire() {
	s.mu.Lock()
	fns := make([]func(), 0, len(s.fns))
	for _, fn := range s.fns {
		fns = append(fns, fn)
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
