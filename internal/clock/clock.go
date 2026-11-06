// Package clock is time as code that schedules work reads it: now, and a function run after a while. The real one
// is the system's; a test's (clocktest) moves only when the test moves it, so what's timed happens exactly then.
package clock

import "time"

// Clock tells the time and runs functions later.
type Clock interface {
	Now() time.Time
	// AfterFunc runs f once d has passed: the system's clock on a goroutine of its own, a test's as it moves past.
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a function waiting to run.
type Timer interface {
	// Stop keeps it from running, saying whether it was still waiting.
	Stop() bool
}

// Real is the system's clock.
func Real() Clock { return real{} }

// Or is c, or the system's clock when c is nil.
func Or(c Clock) Clock {
	if c == nil {
		return real{}
	}
	return c
}

type real struct{}

func (real) Now() time.Time                            { return time.Now() }
func (real) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }
