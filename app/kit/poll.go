package kit

import (
	"context"
	"time"

	"github.com/liamg/oriel/plugin"
)

// UnfocusedPoll is how often network polls run while the window hasn't focus.
const UnfocusedPoll = 5 * time.Minute

// PollEvery runs fn every focused while h's window has focus and every unfocused while it hasn't (never, for 0),
// until ctx ends, noting each wait in the window's polls. With catchUp, focus coming back runs it at once if it's
// due by the focused pace.
func PollEvery(ctx context.Context, h plugin.Host, focused, unfocused time.Duration, catchUp bool, fn func()) {
	PollWith(ctx, h.WindowFocus, PollsOf(h), focused, unfocused, catchUp, fn)
}

// PollWith is PollEvery on a window's focus and polls.
func PollWith(ctx context.Context, focus func() (bool, <-chan struct{}), polls *PollSet, focused, unfocused time.Duration, catchUp bool, fn func()) {
	last := time.Now()
	for {
		on, changed := focus()
		d := unfocused
		if on {
			d = focused
		}
		var tc <-chan time.Time
		stop := func() {}
		if d > 0 {
			t := time.NewTimer(time.Until(last.Add(d)))
			tc = t.C
			unnote := polls.Start(d)
			stop = func() { t.Stop(); unnote() }
		}
		select {
		case <-ctx.Done():
			stop()
			return
		case <-tc:
			stop()
			fn()
			last = time.Now()
		case <-changed:
			stop()
			if on, _ := focus(); catchUp && on && time.Since(last) >= focused {
				fn()
				last = time.Now()
			}
		}
	}
}
