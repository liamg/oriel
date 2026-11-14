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
// in the UI is this spring on a 0..1 progress.
const (
	motionOmega   = 45
	motionSettle  = 0.002
	motionSettleV = 0.05
)

// fx is how a node is drawn this frame besides its layout: faded, moved, its fill eased, its hover wash, and how far
// its content has sunk under a press.
type fx struct {
	opacity float32 // 1 when not fading
	shift   render.Point
	fill    *render.Color
	wash    render.Color
	sink    float32
}

// ringState is the focus ring: the node it's on, where it glides from, and how it's drawn against that node's rect.
type ringState struct {
	id, parent ID
	from       render.Rect
	shown      render.Rect
	delta      render.Rect // shown less the node's rect, corner by corner
	in         float32     // 0..1 as it settles in
}

type easeState struct {
	from, to float32
	seen     int
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

type easeKey struct{ key any }

// Ease glides a value to v on the motion spring, keyed by key under this node, and returns where it is now: a
// meter's fill, a dial's arc. It starts at the first v it's given. A new v mid-glide sets off from where it is,
// keeping its speed, so nothing queues.
func (c *Ctx) Ease(key any, v float32) float32 {
	return c.u.ease(childID(c.e.id, easeKey{key}), v, false)
}

// Snap puts the value Ease keys by key at v at once, and returns it: a move too small or too quick to glide.
func (c *Ctx) Snap(key any, v float32) float32 {
	return c.u.ease(childID(c.e.id, easeKey{key}), v, true)
}

func (u *UI) ease(id ID, v float32, snap bool) float32 {
	if u.eases == nil {
		u.eases = map[ID]*easeState{}
	}
	e, ok := u.eases[id]
	if !ok || snap {
		e = &easeState{from: v, to: v}
		u.eases[id] = e
	}
	e.seen = u.frame
	key := childID(id, motionKey{"ease"})
	if snap {
		delete(u.springs, key)
	}
	if e.to != v {
		p := u.motion(key, 1, 1)
		var speed float32 // in v's units a second
		if sp, ok := u.springs[key]; ok {
			speed = sp.v * (e.to - e.from)
		}
		e.from, e.to = e.from+(e.to-e.from)*p, v
		sp := &spring{x: 0, at: u.now(), seen: u.frame}
		if d := e.to - e.from; d != 0 {
			sp.v = speed / d
		}
		u.springs[key] = sp
	}
	p := u.motion(key, 1, 1)
	return e.from + (e.to-e.from)*p
}

// settled is how far the motion spring has come from rest towards 1, d after it set off: its step response.
func (u *UI) settled(d time.Duration) float32 {
	if u.opts.ReducedMotion || u.opts.MotionSpeed <= 0 || d < 0 {
		if d < 0 {
			return 0
		}
		return 1
	}
	wt := float64(motionOmega*u.opts.MotionSpeed) * d.Seconds()
	x := float32(1 - (1+wt)*math.Exp(-wt))
	if x > 1-motionSettle {
		return 1
	}
	u.wakeAt(u.now().Add(time.Second / 120))
	return x
}

// animate works out each node's motion for this frame, after layout: enters, glides, eased fills, and nodes that
// have gone and are on their way out.
func (u *UI) animate(prev map[ID]*elem) {
	if u.glides == nil {
		u.glides, u.fills, u.exiting = map[ID]*glideState{}, map[ID]*fillState{}, map[ID]*exiting{}
	}
	exitable := u.spare.exitable
	if exitable == nil {
		exitable = map[ID]*elem{}
	}
	clear(exitable)
	for id, e := range u.byID {
		s := e.style
		e.fx = fx{opacity: 1}
		if s.Enter != nil {
			from := float32(1)
			if prev[id] == nil {
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
			case !ok || prev[id] == nil:
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
			case !ok || prev[id] == nil:
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
		u.washes(id, e)
		if s.Exit != nil {
			exitable[id] = e
			delete(u.exiting, id)
		}
	}
	u.focusRing()
	for id, x := range u.eases {
		if x.seen != u.frame {
			delete(u.eases, id)
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
			u.exiting[id] = &exiting{e: e.detach(), tr: *e.style.Exit}
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
	u.spare.exitable, u.exitable = u.exitable, exitable
}

// detach is a copy of e and what's in it off the frame's arena, to draw for as long as it takes to leave.
func (e *elem) detach() *elem {
	c := *e
	c.parent, c.keys = nil, nil
	c.kids = make([]*elem, len(e.kids))
	for i, k := range e.kids {
		c.kids[i] = k.detach()
		c.kids[i].parent = &c
	}
	return &c
}

// washes eases e's hover and press wash, and sinks its content while it's pressed if it asks to (Style.Sink).
func (u *UI) washes(id ID, e *elem) {
	s := e.style
	hot := id != 0 && id == u.in.hot && !s.Disabled
	pressed := hot && u.in.active == id
	if (s.Interactive || s.Focusable) && !s.NoHover && s.Cursor == render.CursorPointer {
		h := u.toward(childID(id, motionKey{"hover"}), hot)
		p := u.toward(childID(id, motionKey{"press"}), pressed)
		if h > 0 || p > 0 {
			w := u.opts.Hover
			w.A = uint8(math.Round(float64(w.A) * float64(h)))
			if p > 0 {
				w = easeColor(w, u.opts.Pressed, p)
			}
			e.fx.wash = w
		}
	}
	if s.Sink {
		e.fx.sink = u.toward(childID(id, motionKey{"sink"}), pressed)
	}
}

// toward runs the 0..1 motion spring id towards on or off, and drops it once it's settled off.
func (u *UI) toward(id ID, on bool) float32 {
	sp, ok := u.springs[id]
	if !on && (!ok || sp.x == 0 && sp.v == 0) {
		delete(u.springs, id)
		return 0
	}
	target := float32(0)
	if on {
		target = 1
	}
	return u.motion(id, 0, target)
}

// focusRing settles the focus ring in on a node focused from the keyboard, and glides it to the next one in the same
// parent.
func (u *UI) focusRing() {
	e, ok := u.byID[u.in.focus]
	if !ok || u.in.focus == 0 || !u.in.visible {
		u.ring.id = 0
		return
	}
	var parent ID
	if e.parent != nil {
		parent = e.parent.id
	}
	r := e.rect()
	appear, glide := childID(0, motionKey{"ring"}), childID(0, motionKey{"ring-glide"})
	if u.ring.id != e.id {
		if u.ring.id != 0 && u.ring.parent == parent {
			u.ring.from = u.ring.shown
			u.springs[glide] = &spring{x: 0, at: u.now(), seen: u.frame}
		} else {
			u.ring.from = r
			u.springs[appear] = &spring{x: 0, at: u.now(), seen: u.frame}
			delete(u.springs, glide)
		}
		u.ring.id, u.ring.parent = e.id, parent
	}
	g := u.motion(glide, 1, 1)
	u.ring.in = u.motion(appear, 0, 1)
	u.ring.shown = render.Rect{Min: lerpPoint(u.ring.from.Min, r.Min, g), Max: lerpPoint(u.ring.from.Max, r.Max, g)}
	u.ring.delta = render.Rect{Min: render.Point{X: u.ring.shown.Min.X - r.Min.X, Y: u.ring.shown.Min.Y - r.Min.Y},
		Max: render.Point{X: u.ring.shown.Max.X - r.Max.X, Y: u.ring.shown.Max.Y - r.Max.Y}}
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

// Still says whether the UI keeps still, with reduced motion or a theme's motion effect at 0: what loops on its own
// (a blink, a scan) doesn't run.
func (c *Ctx) Still() bool { return c.u.opts.ReducedMotion || c.u.opts.MotionSpeed <= 0 }
