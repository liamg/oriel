package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

// Nothing animates for longer than 250 ms.
func TestMotionSettlesWithin250ms(t *testing.T) {
	if d := MotionFor(1); d > 250*time.Millisecond {
		t.Errorf("the motion spring takes %v", d)
	}
}

// motionCase steps a UI's clock through an animation, keeping the display list at the start, a midpoint and once
// it's settled, as one golden.
type motionCase struct {
	t     *testing.T
	u     *UI
	clk   *clock
	build func(c *Ctx)
	out   []string
}

func newMotion(t *testing.T, reduced bool, build func(c *Ctx)) *motionCase {
	u := newUI()
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	o := u.Options()
	o.ReducedMotion = reduced
	u.SetOptions(o)
	m := &motionCase{t: t, u: u, clk: clk, build: build}
	m.frame()
	return m
}

func (m *motionCase) frame() string {
	return describe(paintOps(m.u, render.Point{X: 300, Y: 200}, m.build))
}

// step moves the clock on in 120 Hz frames for d, and records the frame there under name.
func (m *motionCase) step(name string, d time.Duration) {
	var got string
	for el := time.Duration(0); el < d; el += time.Second / 120 {
		m.clk.advance(time.Second / 120)
		got = m.frame()
	}
	if d == 0 {
		got = m.frame()
	}
	m.out = append(m.out, "== "+name+"\n"+got)
}

func (m *motionCase) golden(name string) string {
	m.t.Helper()
	got := strings.Join(m.out, "\n") + "\n"
	golden(m.t, filepath.Join("..", "motion", name), got)
	return got
}

func box(fill uint8) Style {
	return Style{W: Px(40), H: Px(20), Fill: render.Color{R: fill, G: fill, B: fill, A: 255}}
}

// Each transition: the start, a midpoint and the settled frame, and with reduced motion each jumps to settled.
func TestTransitions(t *testing.T) {
	os.MkdirAll(filepath.Join("testdata", "motion"), 0o755)
	for _, reduced := range []bool{false, true} {
		suffix := ""
		if reduced {
			suffix = "-reduced"
		}
		var settled []string
		run := func(name string, build func(show *bool, at *render.Point) func(c *Ctx), change func(show *bool, at *render.Point)) {
			show, at := false, render.Point{X: 10, Y: 10}
			m := newMotion(t, reduced, build(&show, &at))
			change(&show, &at)
			m.step("start", 0)
			m.step("mid", 60*time.Millisecond)
			m.step("settled", 250*time.Millisecond)
			got := m.golden(name + suffix)
			settled = append(settled, got[strings.LastIndex(got, "== settled"):])
			if m.u.animating {
				t.Errorf("%s%s still moves after 250 ms", name, suffix)
			}
			if reduced && !strings.Contains(got, "== start\n"+got[strings.LastIndex(got, "== settled")+len("== settled\n"):]) {
				t.Errorf("%s with reduced motion doesn't start where it settles:\n%s", name, got)
			}
		}
		enter := &Transition{Fade: true, Shift: render.Point{Y: 8}}
		run("enter", func(show *bool, _ *render.Point) func(c *Ctx) {
			return func(c *Ctx) {
				if *show {
					s := box(200)
					s.Enter = enter
					c.Box("menu", s, func(c *Ctx) { c.Text("t", "hi", TextStyle{}) })
				}
			}
		}, func(show *bool, _ *render.Point) { *show = true })
		run("exit", func(show *bool, _ *render.Point) func(c *Ctx) {
			*show = true
			return func(c *Ctx) {
				if *show {
					s := box(200)
					s.Exit = enter
					c.Box("menu", s, func(c *Ctx) { c.Text("t", "hi", TextStyle{}) })
				}
			}
		}, func(show *bool, _ *render.Point) { *show = false })
		run("glide", func(_ *bool, at *render.Point) func(c *Ctx) {
			return func(c *Ctx) {
				s := box(120)
				s.Glide, s.Float = true, &Float{Point: at}
				c.Box("edge", s, nil)
			}
		}, func(_ *bool, at *render.Point) { at.X = 110 })
		run("ease", func(show *bool, _ *render.Point) func(c *Ctx) {
			return func(c *Ctx) {
				s := box(40)
				if *show {
					s = box(240)
				}
				s.EaseFill = true
				c.Box("btn", s, nil)
			}
		}, func(show *bool, _ *render.Point) { *show = true })
		_ = settled
	}
}

// Nothing delays input: a node coming in takes a click where it's laid out at once, and one going out
// takes none.
func TestMotionDoesNotDelayInput(t *testing.T) {
	show := true
	var in Interaction
	build := func(c *Ctx) {
		if show {
			id := c.Next("menu")
			in = c.Interaction(id)
			s := box(200)
			s.Enter, s.Exit, s.Interactive = &Transition{Fade: true, Shift: render.Point{Y: 8}}, &Transition{Fade: true}, true
			c.Box("menu", s, nil)
		}
	}
	m := newMotion(t, false, build)
	m.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 5, Y: 5}, Buttons: render.ButtonPrimary})
	m.u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: 5, Y: 5}, Buttons: render.ButtonPrimary})
	m.frame()
	if !in.Clicked {
		t.Error("a click on a node still coming in was lost")
	}
	show = false
	m.frame()
	if ids := m.u.Hit(render.Point{X: 5, Y: 5}); len(ids) != 0 {
		t.Errorf("a node on its way out takes input: %v", ids)
	}
}

// The theme's motion effect scales the spring: 2 is twice as quick, 0 turns motion off as reduced motion does.
func TestMotionSpeed(t *testing.T) {
	one, two, off := MotionFor(1), MotionFor(2), MotionFor(0)
	if two >= one || off > time.Second/120 {
		t.Errorf("settling at speed 1 %v, 2 %v, 0 %v", one, two, off)
	}
}

// The toolkit's own motion: each starts, passes a midpoint and settles, and with reduced motion jumps.
func TestToolkitMotion(t *testing.T) {
	type scene struct {
		name   string
		build  func(st *toolkitScene) func(c *Ctx)
		setup  func(m *motionCase, st *toolkitScene)
		change func(m *motionCase, st *toolkitScene)
	}
	move := func(m *motionCase, x, y float32) {
		m.u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: x, Y: y}})
	}
	clickable := func(s Style) Style {
		s.Interactive, s.Cursor = true, render.CursorPointer
		return s
	}
	scenes := []scene{
		{"hover", func(*toolkitScene) func(c *Ctx) {
			return func(c *Ctx) { c.Box("btn", clickable(box(40)), nil) }
		}, nil, func(m *motionCase, _ *toolkitScene) { move(m, 5, 5) }},
		{"press-sink", func(*toolkitScene) func(c *Ctx) {
			return func(c *Ctx) {
				s := clickable(box(40))
				s.Sink = true
				c.Box("btn", s, func(c *Ctx) { c.Text("t", "ok", TextStyle{}) })
			}
		}, func(m *motionCase, _ *toolkitScene) {
			move(m, 5, 5)
			m.step("hovered", 300*time.Millisecond)
			m.out = nil
		}, func(m *motionCase, _ *toolkitScene) {
			m.u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 5, Y: 5}, Buttons: render.ButtonPrimary})
		}},
		{"focus-ring", focusPair, nil, func(m *motionCase, _ *toolkitScene) { m.u.FocusNext(1) }},
		{"focus-glide", focusPair, func(m *motionCase, _ *toolkitScene) {
			m.u.FocusNext(1)
			m.step("focused", 300*time.Millisecond)
			m.out = nil
		}, func(m *motionCase, _ *toolkitScene) { m.u.FocusNext(1) }},
		{"tooltip", func(*toolkitScene) func(c *Ctx) {
			return func(c *Ctx) {
				s := box(40)
				s.Tooltip = "Save"
				c.Box("btn", s, nil)
			}
		}, nil, func(m *motionCase, _ *toolkitScene) {
			move(m, 5, 5)
			m.frame()
			m.clk.advance(m.u.opts.TooltipDelay)
		}},
		{"scroll-to", func(st *toolkitScene) func(c *Ctx) {
			return func(c *Ctx) {
				st.id = c.Column("list", Style{W: Px(100), H: Px(60), ScrollY: true, Bars: BarsHidden}, func(c *Ctx) {
					for i := range 12 {
						c.Box(i, box(uint8(20*i)), nil)
					}
				})
			}
		}, nil, func(m *motionCase, st *toolkitScene) { m.u.ScrollTo(st.id, render.Point{Y: 120}) }},
		{"ease-value", func(st *toolkitScene) func(c *Ctx) {
			return func(c *Ctx) {
				c.Box("bar", Style{W: Px(c.Ease("w", st.v)), H: Px(4), Fill: render.Color{R: 255, A: 255}}, nil)
			}
		}, func(_ *motionCase, st *toolkitScene) { st.v = 10 }, func(_ *motionCase, st *toolkitScene) { st.v = 110 }},
	}
	for _, sc := range scenes {
		for _, reduced := range []bool{false, true} {
			name := sc.name
			if reduced {
				name += "-reduced"
			}
			st := &toolkitScene{v: 10}
			m := newMotion(t, reduced, sc.build(st))
			if sc.setup != nil {
				sc.setup(m, st)
			}
			m.frame()
			sc.change(m, st)
			m.step("start", 0)
			m.step("mid", 60*time.Millisecond)
			m.step("settled", 250*time.Millisecond)
			got := m.golden("toolkit-" + name)
			start := got[len("== start\n"):strings.Index(got, "== mid")]
			settled := got[strings.LastIndex(got, "== settled\n")+len("== settled\n"):]
			if m.u.animating {
				t.Errorf("%s still moves after 310 ms", name)
			}
			if reduced && strings.TrimSpace(start) != strings.TrimSpace(settled) {
				t.Errorf("%s: with reduced motion, the first frame isn't the settled one", name)
			}
			if !reduced && strings.TrimSpace(start) == strings.TrimSpace(settled) {
				t.Errorf("%s doesn't move", name)
			}
		}
	}
}

type toolkitScene struct {
	id ID
	v  float32
}

func focusPair(*toolkitScene) func(c *Ctx) {
	return func(c *Ctx) {
		c.Row("row", Style{Gap: 10, Pad: Pad(10)}, func(c *Ctx) {
			for i := range 2 {
				s := box(60)
				s.Focusable = true
				c.Box(i, s, nil)
			}
		})
	}
}

// A ScrollTo past the content's end, asked again mid-glide once the content has grown, gets there: the layout's
// clamp to the shorter content doesn't stick.
func TestScrollToAskedAgainAsTheContentGrows(t *testing.T) {
	var id ID
	n := 10
	m := newMotion(t, false, func(c *Ctx) {
		id = c.Column("list", Style{W: Px(100), H: Px(60), ScrollY: true}, func(c *Ctx) {
			for i := range n {
				c.Box(i, box(10), nil)
			}
		})
		if n > 10 {
			c.u.ScrollTo(id, render.Point{Y: 200})
		}
	})
	m.u.ScrollTo(id, render.Point{Y: 200})
	m.step("clamped", 10*time.Millisecond)
	n = 30
	m.step("grown", 0)
	if got := m.u.Scroll(id).Y; got != 200 {
		t.Errorf("mid-glide, the scroll goes to %v, want 200", got)
	}
	m.step("there", time.Second)
	if got := m.u.ScrollShown(id).Y; got != 200 {
		t.Errorf("the scroll shows %v, want 200", got)
	}
}

// A ScrollTo glides, but Scroll reports where it's going at once, and the wheel takes over from where it's drawn.
func TestScrollToGlides(t *testing.T) {
	var id ID
	m := newMotion(t, false, func(c *Ctx) {
		id = c.Column("list", Style{W: Px(100), H: Px(60), ScrollY: true}, func(c *Ctx) {
			for i := range 30 {
				c.Box(i, box(10), nil)
			}
		})
	})
	m.u.ScrollTo(id, render.Point{Y: 200})
	if got := m.u.Scroll(id).Y; got != 200 {
		t.Errorf("Scroll is %v straight after ScrollTo, want 200", got)
	}
	m.step("mid", 40*time.Millisecond)
	shown := m.u.ScrollShown(id).Y
	if shown <= 140 || shown >= 200 {
		t.Errorf("40 ms in, the scroll shows %v: want it between 140 (a view's length off) and 200", shown)
	}
	m.u.ScrollBy(id, 0, 5)
	m.frame()
	if got := m.u.Scroll(id).Y; got < shown+5-1 || got > shown+5+1 {
		t.Errorf("the wheel mid-glide took the scroll to %v, want %v (from where it was drawn)", got, shown+5)
	}
	if m.u.ScrollShown(id) != m.u.Scroll(id) {
		t.Error("the glide went on after the wheel")
	}
}
