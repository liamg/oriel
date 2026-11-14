package shell_test

import (
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
)

// A click goes to what was on screen when it was made, though what's behind the screen moved since: a list that
// gains a row above, from work that landed just before the click, doesn't move what the click was aimed at.
func TestAClickGoesToTheFrameItWasMadeOn(t *testing.T) {
	ran := make(chan string, 4)
	var host plugin.Host
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Setup: func(h plugin.Host) {
		host = h
		for _, id := range []string{"first", "above"} {
			h.Command(registry.Command{ID: "test." + id, Run: func(registry.Ctx, registry.Args) error { ran <- id; return nil }})
		}
		h.Contribute(uireg.StartEntry{Name: "first", Title: "Say first", Desc: "the one clicked", Command: "test.first", Order: 5})
	}})
	r.TextPoint("Say first") // on screen, and still
	r.Do(func() error {
		// on the loop, so nothing's drawn between: the row above comes in, and the click is made on the frame showing
		fs := r.Win.Frames()
		var at render.Point
		for _, op := range fs[len(fs)-1].Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == "Say first" {
				at = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
			}
		}
		host.Contribute(uireg.StartEntry{Name: "above", Title: "Pushed in above", Desc: "landed just before the click", Command: "test.above", Order: 1})
		r.Click(at)
		return nil
	})
	if got := apptest.Recv(t, "the click to run something", ran); got != "first" {
		t.Errorf("the click ran %q, which moved under it after it was made; it was aimed at first", got)
	}
}
