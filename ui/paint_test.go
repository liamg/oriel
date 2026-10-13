package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
)

func paintOps(u *UI, size render.Point, build func(c *Ctx)) []render.Op {
	var l render.List
	u.Frame(l.Record(1), size, build)
	return l.Ops
}

func describe(ops []render.Op) string {
	var out []string
	for _, op := range ops {
		switch op.Kind {
		case render.OpRect:
			d := "rect " + fmtRect(op.Rect)
			if op.Style.Fill.A > 0 {
				d += fmt.Sprintf(" fill #%02x%02x%02x", op.Style.Fill.R, op.Style.Fill.G, op.Style.Fill.B)
			}
			if op.Style.Width > 0 {
				d += fmt.Sprintf(" stroke %s #%02x%02x%02x", num(op.Style.Width), op.Style.Stroke.R, op.Style.Stroke.G, op.Style.Stroke.B)
			}
			out = append(out, d)
		case render.OpGlyphs:
			out = append(out, "text "+op.Run.Text+" @"+num(op.At.X)+","+num(op.At.Y))
		case render.OpPushClip:
			out = append(out, "clip "+fmtRect(op.Rect))
		case render.OpPushOpacity:
			out = append(out, "opacity "+num(op.Amount))
		case render.OpPop:
			out = append(out, "pop")
		}
	}
	return strings.Join(out, "\n")
}

func TestPaintOrderAndClipping(t *testing.T) {
	fill := Style{Fill: render.Color{R: 1, A: 255}}
	ops := paintOps(newUI(), render.Point{X: 200, Y: 100}, func(c *Ctx) {
		tip := fill
		tip.W, tip.H, tip.Z = Px(30), Px(10), 5
		tip.Float = &Float{Point: &render.Point{X: 150, Y: 0}}
		c.Box("tip", tip, nil)
		pane := fill
		pane.W, pane.H, pane.Clip = Px(100), Px(14), true
		c.Column("pane", pane, func(c *Ctx) {
			c.Text("in", "inside", TextStyle{})
			c.Text("out", "clipped away", TextStyle{})
		})
		low := fill
		low.W, low.H, low.Z = Px(10), Px(10), 1
		low.Float = &Float{Point: &render.Point{X: 0, Y: 90}}
		c.Box("low", low, nil)
	})
	want := strings.Join([]string{
		"rect 0,0 100x14 fill #010000", // the pane, one line tall; floats come after the flow
		"clip 0,0 100x14",
		"text inside @0,11",             // baseline: top plus ascent
		"pop",                           // "clipped away" starts at the clip's bottom edge, so it isn't painted
		"rect 0,90 10x10 fill #010000",  // z=1
		"rect 150,0 30x10 fill #010000", // z=5
	}, "\n")
	if got := describe(ops); got != want {
		t.Errorf("painted:\n%s\nwant:\n%s", got, want)
	}
}

func TestHit(t *testing.T) {
	u := newUI()
	var outer, inner, float ID
	build := func(c *Ctx) {
		outer = c.Box("outer", Style{W: Px(100), H: Px(100), Clip: true}, func(c *Ctx) {
			inner = c.Box("inner", Style{W: Px(300), H: Px(50)}, nil)
			float = c.Box("float", Style{W: Px(20), H: Px(20), Float: &Float{Point: &render.Point{X: 90, Y: 10}}}, nil)
		})
	}
	paintOps(u, render.Point{X: 400, Y: 400}, build)
	for _, tt := range []struct {
		p    render.Point
		want []ID
	}{
		{render.Point{X: 10, Y: 10}, []ID{inner, outer}},
		{render.Point{X: 95, Y: 15}, []ID{float, inner, outer}},
		{render.Point{X: 150, Y: 10}, nil},         // inner reaches here, but outer clips it
		{render.Point{X: 105, Y: 15}, []ID{float}}, // floats aren't clipped by their parent
		{render.Point{X: 10, Y: 70}, []ID{outer}},
	} {
		if got := u.Hit(tt.p); !slices.Equal(got, tt.want) {
			t.Errorf("Hit(%v) = %v, want %v", tt.p, got, tt.want)
		}
	}
	if r, ok := u.Rect(inner); !ok || r != render.XYWH(0, 0, 300, 50) {
		t.Errorf("Rect(inner) = %v, %v", r, ok)
	}
}

func TestStateIsDroppedWhenUnseen(t *testing.T) {
	u := newUI()
	var id ID
	u.Layout(render.Point{X: 100, Y: 100}, func(c *Ctx) { id = c.Box("gone", Style{ScrollY: true}, nil) })
	u.ScrollBy(id, 0, 10)
	u.Layout(render.Point{X: 100, Y: 100}, func(c *Ctx) {})
	if _, ok := u.Rect(id); ok {
		t.Error("an id not built this frame still has state")
	}
}

func list(n int, built *[]int) func(c *Ctx) {
	return func(c *Ctx) {
		c.List("list", Style{W: Flex(1), H: Px(100)}, n, 20, func(c *Ctx, i int) {
			*built = append(*built, i)
			c.Text(nil, "row", TextStyle{})
		})
	}
}

func TestListBuildsOnlyVisibleRows(t *testing.T) {
	u := newUI()
	var built []int
	size := render.Point{X: 200, Y: 300}
	u.Layout(size, list(10_000, &built)) // first frame: no height yet, so up to 64 rows
	id := childID(0, "list")
	built = nil
	u.ScrollBy(id, 0, 1010)
	u.Layout(size, list(10_000, &built))
	if want := []int{50, 51, 52, 53, 54, 55}; !slices.Equal(built, want) {
		t.Errorf("built rows %v, want %v", built, want)
	}
	row, _ := u.Rect(childID(id, 51))
	if row != render.XYWH(0, 10, 200, 20) {
		t.Errorf("row 51 at %v, want 0,10 200x20", row)
	}
	u.ScrollBy(id, 0, 1e9)
	built = nil
	u.Layout(size, list(10_000, &built))
	u.Layout(size, list(10_000, &built))
	if got := u.Scroll(id).Y; got != 10_000*20-100 {
		t.Errorf("scrolled to %v, want the end at %v", got, 10_000*20-100)
	}
	if last := built[len(built)-1]; last != 9_999 {
		t.Errorf("last row built %d", last)
	}
}

// A 10k-row list scrolled a row per frame must build, lay out and paint well inside the 4 ms frame
// budget at 120 Hz (rendering.md).
func TestListScrollBudget(t *testing.T) {
	if raceEnabled || testing.Short() {
		t.Skip("timing under -race or -short means nothing")
	}
	u := newUI()
	size := render.Point{X: 1600, Y: 1000}
	build := func(c *Ctx) {
		c.List("list", Style{W: Flex(1), H: Flex(1)}, 10_000, 21, func(c *Ctx, i int) {
			c.Row(nil, Style{Gap: 8, Pad: PadXY(10, 0), Cross: Center, H: Flex(1)}, func(c *Ctx) {
				c.Text(nil, "func roundHalfEven(v decimal.Decimal, p int32) decimal.Decimal {", TextStyle{})
				c.Text(nil, "← agent · 2m ago", TextStyle{})
			})
		})
	}
	id := childID(0, "list")
	var times []time.Duration
	for range 240 {
		var l render.List
		t0 := time.Now()
		u.Frame(l.Record(1), size, build)
		times = append(times, time.Since(t0))
		u.ScrollBy(id, 0, 21)
	}
	slices.Sort(times)
	p50, p99 := times[len(times)/2], times[len(times)*99/100]
	t.Logf("10k-row list, 48 rows in view: p50 %v, p99 %v per frame", p50, p99)
	if p99 > 4*time.Millisecond {
		t.Errorf("p99 %v per frame, budget 4ms", p99)
	}
}
