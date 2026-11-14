package apptest

import (
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
)

// sceneClock is a scene's clock (registry.Registry.Clock): it reads the scene's Now, and what's timed on it waits
// until the scene's time passes it, with Advance, or Quiet when it's all there is to wait for.
type sceneClock struct {
	now    func() time.Time
	mu     sync.Mutex
	timers []*sceneTimer
}

type sceneTimer struct {
	c  *sceneClock
	at time.Time
	f  func()
}

func (c *sceneClock) Now() time.Time { return c.now() }

func (c *sceneClock) AfterFunc(d time.Duration, f func()) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &sceneTimer{c: c, at: c.now().Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *sceneTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := slices.Contains(t.c.timers, t)
	t.c.timers = slices.DeleteFunc(t.c.timers, func(o *sceneTimer) bool { return o == t })
	return was
}

// next is when the first timer waiting is due, if one is.
func (c *sceneClock) next() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var at time.Time
	for _, t := range c.timers {
		if at.IsZero() || t.at.Before(at) {
			at = t.at
		}
	}
	return at, !at.IsZero()
}

// due takes the first timer due by end, if one is.
func (c *sceneClock) due(end time.Time) (*sceneTimer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := -1
	for j, t := range c.timers {
		if !t.at.After(end) && (i < 0 || t.at.Before(c.timers[i].at)) {
			i = j
		}
	}
	if i < 0 {
		return nil, false
	}
	t := c.timers[i]
	c.timers = slices.Delete(c.timers, i, i+1)
	return t, true
}

// Advance moves the scene's clock on by d, running what's timed on it on the way, each at its own time, in order.
func (s *Scene) Advance(d time.Duration) {
	s.T.Helper()
	end := s.Now.Add(d)
	for {
		t, ok := s.clock.due(end)
		if !ok {
			break
		}
		if t.at.After(s.Now) {
			s.Now = t.at
		}
		t.f()
	}
	s.Now = end
}

// stepClock moves the scene's clock to the first timer waiting on it, if there is one, and runs it.
func (s *Scene) stepClock() bool {
	at, ok := s.clock.next()
	if !ok {
		return false
	}
	s.Advance(max(at.Sub(s.Now), 0))
	return true
}
