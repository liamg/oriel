package ui

import "time"

const (
	FrameInterval = time.Second / 120 // the shortest time between frames
	DecorInterval = time.Second / 15  // the shortest step of a decorative loop: a spinner, typing dots
)

// Pacer spaces frames at least Interval apart (FrameInterval when 0). A frame asked for inside the interval is held
// to its end, never dropped, so the last change is always drawn. The loop owns it.
type Pacer struct {
	Interval time.Duration
	last     time.Time
	asked    bool
}

// Ask notes a change that wants a frame.
func (p *Pacer) Ask() { p.asked = true }

// Due says whether to draw now; when not, at is when the frame asked for falls due, zero with none asked.
func (p *Pacer) Due(now time.Time) (draw bool, at time.Time) {
	if !p.asked {
		return false, time.Time{}
	}
	iv := p.Interval
	if iv == 0 {
		iv = FrameInterval
	}
	at = p.last.Add(iv)
	if !now.Before(at) {
		return true, now
	}
	return false, at
}

// Drew notes a frame drawn at now.
func (p *Pacer) Drew(now time.Time) { p.last, p.asked = now, false }

// SetWindow tells the UI whether its window has the OS's focus and can be seen (not minimised, hidden or covered).
// Decorative loops hold still unless it has both, and a hidden window asks for no frames: what it would have asked
// for is drawn once it shows.
func (u *UI) SetWindow(focused, visible bool) {
	if visible && u.hidden {
		u.shown = true
	}
	u.unfocused, u.hidden = !focused, !visible
}

// WindowFocused is whether the window has the OS's focus.
func (c *Ctx) WindowFocused() bool { return !c.u.unfocused }

// WindowVisible is whether the window can be seen.
func (c *Ctx) WindowVisible() bool { return !c.u.hidden }

// Decorative is where a decorative loop (a spinner, typing dots, a pulse) is: its phase in [0, 1) through period,
// moved in steps steps a period but at most 15 a second, and asking for the frame of the next step. It holds at 0,
// with moving false, under reduced motion and while the window is unfocused or hidden.
func (c *Ctx) Decorative(period time.Duration, steps int) (phase float64, moving bool) {
	if c.Still() || c.u.unfocused || c.u.hidden || period <= 0 {
		return 0, false
	}
	tick := max(period/time.Duration(max(steps, 1)), DecorInterval)
	now := c.Now().Truncate(tick)
	c.WakeAt(now.Add(tick))
	return float64(now.UnixNano()%int64(period)) / float64(period), true
}
