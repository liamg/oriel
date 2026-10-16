package ui

import (
	"math"
	"time"

	"github.com/liamg/oriel/render"
)

// Transition is how a node comes in (Style.Enter) or goes (Style.Exit): it fades, and moves from or to Shift, on
// the motion spring.
type Transition struct {
	Fade  bool
	Shift render.Point
}

// The motion spring: critically damped at ω = 45 rad/s, so a move from 0 to 1 is within 2% by 130 ms and settled
// (under 0.002 away, moving under 0.05 a second) by about 210 ms; Options.MotionSpeed scales it. Every motion
// in the UI (FW-13) is this spring on a 0..1 progress.
const (
	motionOmega   = 45
	motionSettle  = 0.002
	motionSettleV = 0.05
)

// fx is how a node is drawn this frame besides its layout: faded, moved, its fill eased.
type fx struct {
	opacity float32 // 1 when not fading
	shift   render.Point
	fill    *render.Color
}

type glideState struct{ from, to render.Point }

type fillState struct{ from, to render.Color }

// exiting is a node that's gone but still drawn, fading or moving out.
type exiting struct {
	e  *elem
	tr Transition
}

// Motion moves a 0..1 progress towards target on the motion spring keyed by key under this node, and returns where
// it is now. With reduced motion, or a theme's motion effect at 0, it jumps.
func (c *Ctx) Motion(key any, target float32) float32 {
	return c.u.motion(childID(c.e.id, animKey{key}), target, target)
}

// ResetMotion puts the motion spring keyed by key back at x, to run again from there: a glide's new leg.
func (c *Ctx) ResetMotion(key any, x float32) {
	id := childID(c.e.id, animKey{key})
	c.u.springs[id] = &spring{x: x, at: c.u.now(), seen: c.u.frame}
}

// motion is the motion spring for id: from where it starts if it's new, towards target.
func (u *UI) motion(id ID, from, target float32) float32 {
	s, ok := u.springs[id]
	now := u.now()
	speed := u.opts.MotionSpeed
	if !ok {
		s = &spring{x: from, at: now}
		u.springs[id] = s
	}
	s.seen = u.frame
	if u.opts.ReducedMotion || speed <= 0 {
		s.x, s.v, s.at = target, 0, now
		return target
	}
	dt := float32(min(now.Sub(s.at).Seconds(), 0.05))
	s.at = now
	if dt > 0 {
		w := float32(motionOmega) * speed
		k, damp := w*w, 2*w
		const step = 1.0 / 960
		for t := float32(0); t < dt; t += step {
			h := min(step, dt-t)
			s.v += (k*(target-s.x) - damp*s.v) * h
			s.x += s.v * h
		}
	}
	if abs32(target-s.x) < motionSettle && abs32(s.v) < motionSettleV {
		s.x, s.v = target, 0
	} else {
		u.animating = true
	}
	return s.x
}

type motionKey struct{ kind string }

// animate works out each node's motion for this frame, after layout: enters, glides, eased fills, and nodes that
// have gone and are on their way out.
func (u *UI) animate(prev map[ID]bool) {
	if u.glides == nil {
		u.glides, u.fills, u.exiting = map[ID]*glideState{}, map[ID]*fillState{}, map[ID]*exiting{}
	}
	exitable := map[ID]*elem{}
	for id, e := range u.byID {
		s := e.style
		e.fx = fx{opacity: 1}
		if s.Enter != nil {
			from := float32(1)
			if !prev[id] {
				from = 0
			}
			p := u.motion(childID(id, motionKey{"enter"}), from, 1)
			e.fx.apply(*s.Enter, p)
		}
		if s.Glide {
			at := render.Point{X: e.pos[0], Y: e.pos[1]}
			g, ok := u.glides[id]
			key := childID(id, motionKey{"glide"})
			switch {
			case !ok || !prev[id]:
				g = &glideState{from: at, to: at}
				u.glides[id] = g
			case g.to != at:
				p := u.motion(key, 1, 1)
				g.from, g.to = lerpPoint(g.from, g.to, p), at
				u.springs[key] = &spring{x: 0, at: u.now(), seen: u.frame}
			}
			p := u.motion(key, 1, 1)
			shown := lerpPoint(g.from, g.to, p)
			e.fx.shift = addPt(e.fx.shift, render.Point{X: shown.X - at.X, Y: shown.Y - at.Y})
		}
		if s.EaseFill {
			f, ok := u.fills[id]
			key := childID(id, motionKey{"fill"})
			switch {
			case !ok || !prev[id]:
				f = &fillState{from: s.Fill, to: s.Fill}
				u.fills[id] = f
			case f.to != s.Fill:
				p := u.motion(key, 1, 1)
				f.from, f.to = easeColor(f.from, f.to, p), s.Fill
				u.springs[key] = &spring{x: 0, at: u.now(), seen: u.frame}
			}
			p := u.motion(key, 1, 1)
			c := easeColor(f.from, f.to, p)
			e.fx.fill = &c
		}
		if s.Exit != nil {
			exitable[id] = e
			delete(u.exiting, id)
		}
	}
	for id := range u.glides {
		if _, ok := u.byID[id]; !ok {
			delete(u.glides, id)
		}
	}
	for id := range u.fills {
		if _, ok := u.byID[id]; !ok {
			delete(u.fills, id)
		}
	}
	for id, e := range u.exitable {
		if _, still := u.byID[id]; !still && !u.insideExiting(e) {
			u.exiting[id] = &exiting{e: e, tr: *e.style.Exit}
		}
	}
	for id, x := range u.exiting {
		p := u.motion(childID(id, motionKey{"exit"}), 1, 0)
		if p <= 0 {
			delete(u.exiting, id)
			continue
		}
		x.e.fx = fx{opacity: 1}
		x.e.fx.apply(x.tr, p)
	}
	u.exitable = exitable
}

// insideExiting reports whether e is in a subtree already on its way out, which draws it.
func (u *UI) insideExiting(e *elem) bool {
	for p := e.parent; p != nil; p = p.parent {
		if _, ok := u.exiting[p.id]; ok {
			return true
		}
		if p.style.Exit != nil {
			if _, still := u.byID[p.id]; !still {
				return true
			}
		}
	}
	return false
}

// apply draws a transition at progress p: 0 is where it comes from or goes to, 1 is the node as laid out.
func (f *fx) apply(t Transition, p float32) {
	if t.Fade {
		f.opacity *= max(0, min(1, p))
	}
	f.shift = addPt(f.shift, render.Point{X: t.Shift.X * (1 - p), Y: t.Shift.Y * (1 - p)})
}

func addPt(a, b render.Point) render.Point { return render.Point{X: a.X + b.X, Y: a.Y + b.Y} }

func lerpPoint(a, b render.Point, p float32) render.Point {
	return render.Point{X: a.X + (b.X-a.X)*p, Y: a.Y + (b.Y-a.Y)*p}
}

// easeColor is p of the way from a to b; a transparent end takes the other's colour, so a fill fades in or out
// rather than through black.
func easeColor(a, b render.Color, p float32) render.Color {
	if p >= 1 {
		return b
	}
	if a.A == 0 {
		a = render.Color{R: b.R, G: b.G, B: b.B}
	}
	if b.A == 0 {
		b = render.Color{R: a.R, G: a.G, B: a.B}
	}
	l := func(x, y uint8) uint8 { return uint8(math.Round(float64(x) + (float64(y)-float64(x))*float64(p))) }
	return render.Color{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// MotionFor is how long the motion spring takes to settle a 0..1 move at speed, for tests and budgets.
func MotionFor(speed float32) time.Duration {
	u := New(nil, TextStyle{})
	u.opts.MotionSpeed = speed
	now := time.Unix(0, 0)
	u.SetClock(func() time.Time { return now })
	id := ID(1)
	u.motion(id, 0, 1)
	for t := time.Duration(0); t < 2*time.Second; t += time.Second / 120 {
		now = now.Add(time.Second / 120)
		u.animating = false
		u.motion(id, 0, 1)
		if !u.animating {
			return t + time.Second/120
		}
	}
	return 2 * time.Second
}

// Still says whether the UI keeps still, with reduced motion or a theme's motion effect at 0: what loops on its own
// (a blink, a scan) doesn't run.
func (c *Ctx) Still() bool { return c.u.opts.ReducedMotion || c.u.opts.MotionSpeed <= 0 }
