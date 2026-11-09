package agentui_test

import (
	"testing"

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
	// Sent at once, as a quick drag reaches a busy loop: the release among moves no frame has been built with yet.
	send := func(k render.PointerKind, dx float32, b render.Buttons) {
		r.Win.Send(render.PointerEvent{Kind: k, Pos: render.Point{X: x + dx, Y: y}, Buttons: b})
	}
	send(render.PointerMove, 0, 0)
	send(render.PointerPress, 0, render.ButtonPrimary)
	for dx := float32(10); dx <= 100; dx += 10 {
		send(render.PointerMove, dx, render.ButtonPrimary)
	}
	send(render.PointerRelease, 100, render.ButtonPrimary)
	// The release goes in once a frame has been built with the last move: the split has moved once the loop has
	// taken them all.
	r.WaitFor("a 100px drag to move the split 80px or more", func() bool {
		w := r.Host.Layout().Main.Weights
		return w[0]/(w[0]+w[1])*width-left.Dx() >= 80
	})
}
