package panes

import (
	"testing"

	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// A pane's keys are read as it's built, and they can close the panes beside it (an editor's CTRL-W o): the split
// collapses while the frame walks it, and the frame finishes; the next shows only the pane that's left.
func TestAPaneClosingItsSiblingsWhileBuilt(t *testing.T) {
	h := newHarness(t)
	h.r = layout.New()
	va, vb, vc := layout.ViewRef{ID: "e", Instance: "1"}, layout.ViewRef{ID: "e", Instance: "2"}, layout.ViewRef{ID: "e", Instance: "3"}
	for _, op := range []struct {
		v  layout.ViewRef
		at *layout.ViewRef
	}{{va, nil}, {vb, &va}, {vc, &vb}} {
		p, o := layout.Tab, layout.Opts{}
		if op.at != nil {
			p, o = layout.Split, layout.Opts{Beside: op.at, Side: layout.SideLeft}
		}
		if err := h.r.Open(op.v, p, o); err != nil {
			t.Fatal(err)
		}
	}
	only := true
	frame := func() (built []layout.ViewRef) {
		h.u.Frame(h.list.Record(1), screen, func(c *ui.Ctx) {
			View(c, "layout", h.r, &h.panes, func(c *ui.Ctx, v layout.ViewRef, focused bool) {
				built = append(built, v)
				if v == vc && only {
					only = false
					h.r.Close(vb)
					h.r.Close(va)
				}
				c.Text("name", v.String(), ui.TextStyle{})
			})
		})
		return built
	}
	_ = frame()
	if got := frame(); len(got) != 1 || got[0] != vc {
		t.Fatalf("the frame after shows %v, want only %v", got, vc)
	}
}
