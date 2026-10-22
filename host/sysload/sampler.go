package sysload

import (
	"context"
	"math"
	"sync"
	"time"
)

// Step is how far, in percentage points of CPU or memory, a reading must move to be news.
const Step = 1.0

// Sampler reads the machine every interval while anyone wants it, and says when the reading moved by Step or more.
// With no one wanting it, it reads nothing and sleeps.
type Sampler struct {
	every   time.Duration
	read    func() Reading
	changes chan Reading
	wake    chan struct{}

	mu     sync.Mutex
	wanted int
	last   Reading
	got    bool // last is set
	sent   Reading
	have   bool // sent is set
}

// NewSampler is a sampler reading with read (a Reader of its own if nil) every interval, once Run.
func NewSampler(every time.Duration, read func() Reading) *Sampler {
	if read == nil {
		read = NewReader().Read
	}
	return &Sampler{every: every, read: read, changes: make(chan Reading, 1), wake: make(chan struct{}, 1)}
}

// Want asks for readings until release is called; it reads at once when it starts. Calls nest.
func (s *Sampler) Want() (release func()) {
	s.mu.Lock()
	s.wanted++
	s.mu.Unlock()
	s.poke()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			s.wanted--
			s.mu.Unlock()
			s.poke()
		})
	}
}

// Wanted is whether anyone wants readings now.
func (s *Sampler) Wanted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wanted > 0
}

// Last is the latest reading: read now only when there's none yet.
func (s *Sampler) Last() Reading {
	s.mu.Lock()
	r, ok := s.last, s.got
	s.mu.Unlock()
	if ok {
		return r
	}
	r = s.read()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.got {
		s.last, s.got = r, true
	}
	return s.last
}

// Changes are the readings that moved by Step or more, the latest only when they come faster than they're taken.
func (s *Sampler) Changes() <-chan Reading { return s.changes }

func (s *Sampler) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Run samples until ctx ends.
func (s *Sampler) Run(ctx context.Context) {
	var tick *time.Ticker
	var tc <-chan time.Time
	defer func() {
		if tick != nil {
			tick.Stop()
		}
	}()
	for {
		on := s.Wanted()
		switch {
		case on && tick == nil:
			tick = time.NewTicker(s.every)
			tc = tick.C
			s.sample()
		case !on && tick != nil:
			tick.Stop()
			tick, tc = nil, nil
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tc:
			s.sample()
		}
	}
}

func (s *Sampler) sample() {
	r := s.read()
	s.mu.Lock()
	s.last, s.got = r, true
	news := !s.have || moved(s.sent, r)
	if news {
		s.sent, s.have = r, true
	}
	s.mu.Unlock()
	if !news {
		return
	}
	select {
	case <-s.changes:
	default:
	}
	s.changes <- r
}

// moved is b different enough from a to be news.
func moved(a, b Reading) bool {
	if a.Err != b.Err || a.Cores != b.Cores || a.MemTotal != b.MemTotal {
		return true
	}
	return math.Abs(a.CPU-b.CPU) >= Step || math.Abs(memPercent(a)-memPercent(b)) >= Step
}

func memPercent(r Reading) float64 {
	if r.MemTotal == 0 {
		return 0
	}
	return float64(r.MemUsed) / float64(r.MemTotal) * 100
}
