//go:build perf

package ui

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/tools/perf"
)

// 200 hover targets under a moving pointer: a frame with input still fits the 4 ms budget at 120 Hz.
func TestHoverTargetsBudget(t *testing.T) {
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing under -race, -short or -cover means nothing")
	}
	u := newUI()
	build := func(c *Ctx) {
		c.Column("grid", Style{W: Flex(1), H: Flex(1), Pad: Pad(10), Gap: 4}, func(c *Ctx) {
			for r := range 20 {
				c.Row(r, Style{Gap: 4}, func(c *Ctx) {
					for i := range 10 {
						key := r*10 + i
						in := c.Interaction(c.Next(key))
						fill := idle
						if in.Hovered {
							fill = hovered
						}
						c.Row(key, Style{W: Px(120), H: Px(22), Pad: PadXY(8, 4), Fill: fill, Radius: 4, Focusable: true,
							Cursor: render.CursorPointer, Tooltip: "target"}, func(c *Ctx) {
							c.Text(nil, fmt.Sprintf("target %d", key), TextStyle{})
						})
					}
				})
			}
		})
	}
	// Each frame's time is its best over three runs of the same moves (perf.Times): one the scheduler preempted
	// isn't slow, one that's slow every time is.
	times := perf.Times(3, 240, nil, func(i int) {
		var l render.List
		u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: float32(10 + i*5%1200), Y: float32(10 + i*3%500)}})
		u.Frame(l.Record(1), render.Point{X: 1600, Y: 1000}, build)
		_ = u.Cursor()
	})
	slices.Sort(times)
	p50, p99 := times[len(times)/2], times[len(times)*99/100]
	t.Logf("200 hover targets: p50 %v, p99 %v per frame", p50, p99)
	if p99 > 4*time.Millisecond {
		t.Errorf("p99 %v, budget 4ms", p99)
	}
}

// A 10k-row list scrolled a row per frame must build, lay out and paint well inside the 4 ms frame
// budget at 120 Hz.
func TestListScrollBudget(t *testing.T) {
	if raceEnabled || testing.Short() || testing.CoverMode() != "" {
		t.Skip("timing under -race, -short or -cover means nothing")
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
	times := perf.Times(3, 240, func() { u.ScrollBy(id, 0, -1e9) }, func(int) {
		var l render.List
		u.Frame(l.Record(1), size, build)
		u.ScrollBy(id, 0, 21)
	})
	p50, p99 := perf.Pct(times, 50), perf.Pct(times, 99)
	t.Logf("10k-row list, 48 rows in view: p50 %v, p99 %v per frame", p50, p99)
	if p99 > 4*time.Millisecond {
		t.Errorf("p99 %v per frame, budget 4ms", p99)
	}
}
