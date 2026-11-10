package sysload

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
)

// Step is how far, in percentage points of CPU or memory, a reading must move to be news.
const Step = 1.0

// Sampler reads the machine every interval while anyone wants it, and says when the reading moved by Step or more.
// With no one wanting it, it reads nothing and sleeps.
type Sampler struct {
	// Clock times the readings; the system's if nil. Set before Run.
	Clock clock.Clock

	every   time.Duration
	read    func() Reading
	changes chan Reading
	wake    chan struct{}
	sendMu  sync.Mutex // one reading sent at a time

	mu     sync.Mutex
	wanted int
	timer  clock.Timer // the next reading, while wanted
	gen    int         // bumped as the readings stop: a timer of an older one does nothing
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

// Want asks for readings until release is called; it reads at once when it starts. Calls nest. The last release
// stops the readings before it returns.
func (s *Sampler) Want() (release func()) {
	s.mu.Lock()
	s.wanted++
	s.mu.Unlock()
	s.poke()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			if s.wanted--; s.wanted == 0 {
				s.stopLocked()
			}
			s.mu.Unlock()
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

// Run samples until ctx ends: at once when wanted, then every interval while it still is.
func (s *Sampler) Run(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.stopLocked()
		s.mu.Unlock()
	}()
	for {
		s.mu.Lock()
		start := s.wanted > 0 && s.timer == nil
		if start {
			s.scheduleLocked()
		}
		s.mu.Unlock()
		if start {
			s.sample()
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		}
	}
}

// scheduleLocked sets the next reading. Callers hold s.mu.
func (s *Sampler) scheduleLocked() {
	gen := s.gen
	s.timer = clock.Or(s.Clock).AfterFunc(s.every, func() { s.tick(gen) })
}

// stopLocked stops the readings. Callers hold s.mu.
func (s *Sampler) stopLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.gen++
}

func (s *Sampler) tick(gen int) {
	s.mu.Lock()
	on := gen == s.gen
	s.mu.Unlock()
	if !on {
		return
	}
	s.sample()
	s.mu.Lock()
	if gen == s.gen {
		s.scheduleLocked()
	}
	s.mu.Unlock()
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
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
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
