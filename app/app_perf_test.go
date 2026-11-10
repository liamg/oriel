//go:build perf

package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// Sweeping down a menu, each row's highlight eases in while the pointer goes on moving over it: moves that change
// nothing build no frame, but mustn't hold back the frames the easing needs.
func TestMenuHoverWhileThePointerMovesBudget(t *testing.T) {
	b := boot(t, menuView{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b.win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), "Close All") })
	for n := len(b.win.Frames()); ; {
		time.Sleep(200 * time.Millisecond)
		if m := len(b.win.Frames()); m == n || ctx.Err() != nil {
			break
		} else {
			n = m
		}
	}
	// The root view sits under the 38px titlebar, right of the 44px rail; the menu's rows are 30px under 4px of pad.
	row := func(i int) render.Point { return render.Point{X: 44 + 100, Y: 38 + 4 + 15 + 30*float32(i)} }
	frames := b.win.Frames()
	last := frames[len(frames)-1]
	bg := fillAt(last, row(2), render.Color{})
	hover := fillAt(last, row(0), bg) // the first row starts highlighted
	if hover.A == 0 {
		t.Fatal("no highlight on the first row")
	}
	near := func(a, b render.Color) bool {
		d := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 2 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
	}
	const stay, every = 400 * time.Millisecond, 2 * time.Millisecond
	for i := 1; i < 4; i++ {
		start := len(b.win.Frames())
		for j := range int(stay / every) {
			b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: row(i).X + float32(j%20), Y: row(i).Y}})
			time.Sleep(every)
		}
		moving := b.win.Frames()[start:]
		var got render.Color
		if len(moving) > 0 {
			got = fillAt(moving[len(moving)-1], row(i), bg)
		}
		if !near(got, hover) {
			t.Errorf("after %v of moves over row %d it's drawn %v in %d frames, not the highlight %v", stay, i, got, len(moving), hover)
		}
	}
	b.close(t)
}
