package ui

import (
	"slices"
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

func TestDecorativeStepsAtMost8HzAndStopsUnfocused(t *testing.T) {
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
	if frames > 8 {
		t.Fatalf("%d frames in a second, over 8", frames)
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

// WokenBy names the element whose loop asked for the next frame, by its keys.
func TestWokenByNamesTheLoop(t *testing.T) {
	u := newUI()
	u.opts.ReducedMotion = false
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	paintOps(u, render.Point{X: 100, Y: 100}, func(c *Ctx) {
		c.Column("panel", Style{}, func(c *Ctx) {
			c.Row("status", Style{}, func(c *Ctx) { c.Decorative(time.Second, 10) })
		})
	})
	if got := u.WokenBy(); got != "panel/status" {
		t.Fatalf("woken by %q, want panel/status", got)
	}
	paintOps(u, render.Point{X: 100, Y: 100}, func(c *Ctx) { c.Box("still", Style{}, nil) })
	if got := u.WokenBy(); got != "" {
		t.Fatalf("a still frame woken by %q", got)
	}
}

// A loop read in paint steps by painting the last layout again: the build doesn't run, and each repaint draws the
// phase of its own moment.
func TestAnimateRepaintsWithoutBuilding(t *testing.T) {
	u := newUI()
	u.opts.ReducedMotion = false
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	builds := 0
	var drawn []float64
	build := func(c *Ctx) {
		builds++
		loop := c.Animate(time.Second, 4)
		c.Paint("spin", Px(10), Px(10), func(render.Frame, render.Rect) { drawn = append(drawn, loop.Phase()) })
	}
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	for range 3 {
		next := u.NextFrame()
		if next.IsZero() {
			t.Fatal("a moving loop asked for no frame")
		}
		clk.t = next
		if !u.Repaintable(clk.t) {
			t.Fatal("a frame for a loop's step needs a build")
		}
		var l render.List
		u.Repaint(l.Record(1))
	}
	if builds != 1 {
		t.Fatalf("built %d times, want 1", builds)
	}
	if want := []float64{0, .25, .5, .75}; !slices.Equal(drawn, want) {
		t.Fatalf("painted phases %v, want %v", drawn, want)
	}
	u.wakeAt(clk.t)
	if u.Repaintable(clk.t) {
		t.Fatal("a frame something else asked for is only repainted")
	}
}

// A loop scrolled out of sight in the last frame holds still and asks for no frames.
func TestAnimateOutOfSightIsStill(t *testing.T) {
	u := newUI()
	u.opts.ReducedMotion = false
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	moving := false
	build := func(c *Ctx) {
		c.Column("list", Style{W: Px(50), H: Px(20), ScrollY: true}, func(c *Ctx) {
			c.Box("filler", Style{H: Px(100)}, nil)
			c.Box("spin", Style{W: Px(10), H: Px(10)}, func(c *Ctx) { moving = c.Animate(time.Second, 4).Moving() })
		})
	}
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	paintOps(u, render.Point{X: 100, Y: 100}, build)
	if moving || !u.NextFrame().IsZero() {
		t.Fatalf("out of sight: moving %v, next frame %v", moving, u.NextFrame())
	}
}
