package ui

import (
	"time"

	"github.com/liamg/oriel/render"
)

const (
	FrameInterval = time.Second / 120 // the shortest time between frames
	DecorInterval = time.Second / 8   // the shortest step of a decorative loop: a spinner, typing dots
)

// Pacer spaces frames at least Interval apart (FrameInterval when 0). A frame asked for inside the interval is held
// to its end, never dropped, so the last change is always drawn. The loop owns it.
type Pacer struct {
	Interval time.Duration
	last     time.Time
	asked    bool
	full     bool // asked for a frame built again, not only a repaint
}

// Ask notes a change that wants a frame built.
func (p *Pacer) Ask() { p.asked, p.full = true, true }

// AskRepaint notes a decorative loop's next step: a frame painted again from the last layout will do (UI.Repaint).
func (p *Pacer) AskRepaint() { p.asked = true }

// Full says the frame asked for must be built, not only painted again.
func (p *Pacer) Full() bool { return p.full }

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
func (p *Pacer) Drew(now time.Time) { p.last, p.asked, p.full = now, false, false }

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

// Decorative is where a decorative loop (a pulse that changes styles) is: its phase in [0, 1) through period, moved
// in steps steps a period but at most 8 a second, and asking for the frame of the next step, built again. It holds at
// 0, with moving false, under reduced motion, while the window is unfocused or hidden, and while what's building it
// was out of sight in the last frame. A loop drawn in a Paint takes Animate instead, which only repaints.
func (c *Ctx) Decorative(period time.Duration, steps int) (phase float64, moving bool) {
	l := c.Animate(period, steps)
	if !l.Moving() {
		return 0, false
	}
	c.WakeAt(c.Now().Truncate(l.tick).Add(l.tick))
	return l.Phase(), true
}

// Loop is a decorative loop read while painting.
type Loop struct {
	u            *UI
	period, tick time.Duration
}

// Moving says the loop moves; a still one's Phase is 0.
func (l Loop) Moving() bool { return l.u != nil }

// Phase is where the loop is now, in [0, 1) through its period, in its steps.
func (l Loop) Phase() float64 {
	if l.u == nil {
		return 0
	}
	now := l.u.now().Truncate(l.tick)
	return float64(now.UnixNano()%int64(l.period)) / float64(l.period)
}

// Animate starts a decorative loop (a spinner, typing dots) whose Phase a Paint reads as it draws: its steps, at most
// 8 a second, are drawn by painting the last layout again (UI.Repaint), not building it. It holds still as Decorative
// does.
func (c *Ctx) Animate(period time.Duration, steps int) Loop {
	if c.Still() || c.u.unfocused || c.u.hidden || period <= 0 || !c.u.shownLast(c.e.id) {
		return Loop{}
	}
	l := Loop{u: c.u, period: period, tick: max(period/time.Duration(max(steps, 1)), DecorInterval)}
	c.u.loops = append(c.u.loops, l.tick)
	if next := c.Now().Truncate(l.tick).Add(l.tick); c.u.loopWake.IsZero() || next.Before(c.u.loopWake) {
		c.u.loopWake, c.u.looper = next, c.e
	}
	return l
}

// shownLast says id was in sight in the last frame, inside the clips of what held it, or wasn't in it at all. One
// with no area can't tell, so counts as shown.
func (u *UI) shownLast(id ID) bool {
	e, ok := u.spare.byID[id]
	if !ok || e.size[0] <= 0 || e.size[1] <= 0 {
		return true
	}
	r := e.rect()
	if root := u.spare.byID[0]; root != nil && root != e {
		r = intersect(r, root.rect())
	}
	for p := e; p.parent != nil && (p.style.Float == nil || p.inlaid); p = p.parent {
		if s := p.parent.style; s.Clip || s.ScrollX || s.ScrollY {
			r = intersect(r, p.parent.rect())
		}
	}
	return r.Dx() > 0 && r.Dy() > 0
}

// Repaintable says the frame due at now only steps decorative loops: Repaint will draw it.
func (u *UI) Repaintable(now time.Time) bool {
	return !u.hidden && !u.shown && !u.animating && !u.in.pending() && u.root != nil &&
		!u.loopWake.IsZero() && !u.loopWake.After(now) && (u.wake.IsZero() || u.wake.After(now))
}

// Repaint paints the last layout again into f, for the loops' next steps, and sets when they next step.
func (u *UI) Repaint(f render.Frame) {
	u.Paint(f)
	now := u.now()
	u.loopWake = time.Time{}
	for _, tick := range u.loops {
		if next := now.Truncate(tick).Add(tick); u.loopWake.IsZero() || next.Before(u.loopWake) {
			u.loopWake = next
		}
	}
}
