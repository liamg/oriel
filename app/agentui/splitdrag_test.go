package agentui_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// A splitter between two views moves as far as it's dragged, not one step: the layout made again as it changes is
// the same split.
func TestSplitterDragsAllTheWay(t *testing.T) {
	r, _, _ := apptest.AgentWindow(t)
	r.Run("view.open", registry.Args{"id": kit.RequestView, "place": "tab", "title": "Request"})
	r.Run("view.open", registry.Args{"id": kit.AgentConsoleView, "place": "split", "side": "right", "title": "Agent"})
	var left render.Rect
	var width float32
	for deadline := time.Now().Add(5 * time.Second); left.Dx() == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the split never showed")
		}
		r.Do(func() error {
			for _, v := range r.Host.Layout().All() {
				if v.ID == kit.RequestView {
					left, _ = r.Host.PaneRect(v)
				}
				if v.ID == kit.AgentConsoleView {
					rc, _ := r.Host.PaneRect(v)
					width = left.Dx() + rc.Dx()
				}
			}
			return nil
		})
	}
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
	var share float32
	r.Do(func() error {
		w := r.Host.Layout().Main.Weights
		share = w[0] / (w[0] + w[1])
		return nil
	})
	if moved := share*width - left.Dx(); moved < 80 {
		t.Errorf("a 100px drag moved the split %.0fpx", moved)
	}
}
