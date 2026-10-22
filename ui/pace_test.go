package ui

import (
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

func TestPacerHoldsTheTrailingFrame(t *testing.T) {
	var p Pacer
	t0 := time.Unix(1000, 0)
	if draw, at := p.Due(t0); draw || !at.IsZero() {
		t.Fatalf("nothing asked, yet due: %v %v", draw, at)
	}
	p.Ask()
	if draw, _ := p.Due(t0); !draw {
		t.Fatal("the first frame waits")
	}
	p.Drew(t0)
	p.Ask()
	draw, at := p.Due(t0.Add(time.Millisecond))
	if draw || !at.Equal(t0.Add(FrameInterval)) {
		t.Fatalf("a frame 1ms after the last: draw %v, at %v; want held to %v", draw, at, t0.Add(FrameInterval))
	}
	p.Ask() // more changes in the interval share the held frame
	if draw, _ := p.Due(t0.Add(FrameInterval)); !draw {
		t.Fatal("the held frame isn't drawn when it falls due")
	}
	p.Drew(t0.Add(FrameInterval))
	if draw, at := p.Due(t0.Add(time.Second)); draw || !at.IsZero() {
		t.Fatal("a frame drawn with nothing asked since")
	}
}

func TestDecorativeStepsAtMost15HzAndStopsUnfocused(t *testing.T) {
	u := newUI()
	u.opts.ReducedMotion = false
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	var phase float64
	var moving bool
	build := func(c *Ctx) { phase, moving = c.Decorative(time.Second, 100) }
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if !moving {
		t.Fatal("a focused window's loop is still")
	}
	frames, end := 0, clk.t.Add(time.Second)
	for next := u.NextFrame(); next.Before(end); next = u.NextFrame() {
		clk.t = next
		paintOps(u, render.Point{X: 100, Y: 100}, build)
		frames++
	}
	if frames > 15 {
		t.Fatalf("%d frames in a second, over 15", frames)
	}
	clk.t = time.Unix(1000, 0).Add(250 * time.Millisecond)
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if phase < .2 || phase > .3 {
		t.Fatalf("phase %v a quarter of the way through", phase)
	}

	u.SetWindow(false, true)
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if moving || phase != 0 || !u.NextFrame().IsZero() {
		t.Fatalf("unfocused: moving %v, phase %v, next frame %v; want still with no frames", moving, phase, u.NextFrame())
	}
	u.SetWindow(true, false)
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if moving || !u.NextFrame().IsZero() {
		t.Fatal("a hidden window's loop asks for frames")
	}
}

func TestHiddenWindowHoldsWakeUps(t *testing.T) {
	u := newUI()
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	u.SetWindow(true, false)
	paintOps(u, render.Point{X: 100, Y: 100}, func(c *Ctx) { c.WakeAt(clk.t.Add(time.Second)) })
	if !u.NextFrame().IsZero() {
		t.Fatal("a hidden window asked for a frame")
	}
	u.SetWindow(true, true)
	if u.NextFrame().IsZero() {
		t.Fatal("the held wake-up was lost when the window showed")
	}
}
