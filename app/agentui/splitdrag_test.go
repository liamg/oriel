package agentui_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// A splitter between two views moves as far as it's dragged, not one step: the layout made again as it changes is
// the same split.
func TestSplitterDragsAllTheWay(t *testing.T) {
	r, _, _ := apptest.AgentWindow(t)
	r.Run("view.open", registry.Args{"id": kit.RequestView, "place": "tab", "title": "Request"})
	r.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "split", "side": "right", "title": "Agent"})
	type split struct {
		left  render.Rect
		width float32
	}
	at := apptest.Settled(r, "the split", func(headless.Frame) (split, bool) {
		var sp split
		r.Do(func() error {
			for _, v := range r.Host.Layout().All() {
				if v.ID == kit.RequestView {
					sp.left, _ = r.Host.PaneRect(v)
				}
				if v.ID == kit.AgentConsoleView {
					rc, _ := r.Host.PaneRect(v)
					sp.width = sp.left.Dx() + rc.Dx()
				}
			}
			return nil
		})
		return sp, sp.left.Dx() > 0 && sp.width > sp.left.Dx()
	})
	left, width := at.left, at.width
	x, y := left.Max.X, left.Min.Y+300
	send := func(k render.PointerKind, dx float32, b render.Buttons) {
		r.Win.Send(render.PointerEvent{Kind: k, Pos: render.Point{X: x + dx, Y: y}, Buttons: b})
		time.Sleep(20 * time.Millisecond)
	}
	send(render.PointerMove, 0, 0)
	send(render.PointerPress, 0, render.ButtonPrimary)
	for dx := float32(10); dx <= 100; dx += 10 {
		send(render.PointerMove, dx, render.ButtonPrimary)
	}
	send(render.PointerRelease, 100, 0)
	moved := func() float32 {
		var share float32
		r.Do(func() error {
			w := r.Host.Layout().Main.Weights
			share = w[0] / (w[0] + w[1])
			return nil
		})
		return share*width - left.Dx()
	}
	// The loop takes the moves sent before the release, then the release: the split has moved once it has.
	for deadline := time.Now().Add(apptest.Timeout); moved() < 80; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("a 100px drag moved the split %.0fpx", moved())
		}
	}
}
