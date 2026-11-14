// Package clocktest is a clock a test moves: nothing scheduled on it runs until Advance passes its time.
package clocktest

import (
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
)

// Fake is a clock that stands still until Advance.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*timer
}

type timer struct {
	c       *Fake
	at      time.Time
	f       func()
	stopped bool
}

// New is a clock at start.
func New(start time.Time) *Fake { return &Fake{now: start} }

func (c *Fake) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Fake) AfterFunc(d time.Duration, f func()) clock.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &timer{c: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

func (t *timer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.stopped && slices.Contains(t.c.timers, t)
	t.stopped = true
	t.c.timers = slices.DeleteFunc(t.c.timers, func(o *timer) bool { return o == t })
	return was
}

// Advance moves the clock on by d, running each function due on the way, in the order they're due, each at its own
// time and to its end before the next: what one schedules within d runs too.
func (c *Fake) Advance(d time.Duration) {
	c.mu.Lock()
	end := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		i := -1
		for j, t := range c.timers {
			if !t.at.After(end) && (i < 0 || t.at.Before(c.timers[i].at)) {
				i = j
			}
		}
		if i < 0 {
			c.now = end
			c.mu.Unlock()
			return
		}
		t := c.timers[i]
		c.timers = slices.Delete(c.timers, i, i+1)
		if t.at.After(c.now) {
			c.now = t.at
		}
		c.mu.Unlock()
		t.f()
	}
}

// Set puts the clock at t: on, as Advance does, or back, running nothing.
func (c *Fake) Set(t time.Time) {
	c.mu.Lock()
	d := t.Sub(c.now)
	if d < 0 {
		c.now = t
	}
	c.mu.Unlock()
	if d >= 0 {
		c.Advance(d)
	}
}

// Next is when the first function waiting is due, if one is.
func (c *Fake) Next() (time.Time, bool) {
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

// Waiting counts the functions not run yet.
func (c *Fake) Waiting() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.timers)
}
