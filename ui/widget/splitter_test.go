package widget

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func splitScene(t *testing.T, st *SplitState, o SplitterOpts) *harness {
	return newHarness(t, theme.Dark, render.Point{X: 800, Y: 300}, func(c *ui.Ctx) {
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Box("list", ui.Style{W: ui.Px(st.Width(o)), H: ui.Flex(1)}, nil)
			Splitter(c, "split", st, o)
			c.Box("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, nil)
		})
	})
}

// The line after a box is dragged sideways to resize it, between its Min and Max; a double click puts it back to
// its Default.
func TestSplitterResizesTheBoxBeforeIt(t *testing.T) {
	st := &SplitState{}
	o := SplitterOpts{Default: 300, Min: 200, Max: 500, Label: "Resize the list"}
	h := splitScene(t, st, o)
	h.frame()
	if w := st.Width(o); w != 300 {
		t.Fatalf("it opened %v wide, want its Default 300", w)
	}
	h.drag(render.Point{X: 300, Y: 150}, render.Point{X: 380, Y: 150})
	if w := st.Width(o); w != 380 {
		t.Errorf("dragged 80 right, it's %v wide, want 380", w)
	}
	h.drag(render.Point{X: 380, Y: 150}, render.Point{X: 50, Y: 150})
	if w := st.Width(o); w != 200 {
		t.Errorf("dragged past its Min, it's %v wide, want 200", w)
	}
	h.drag(render.Point{X: 200, Y: 150}, render.Point{X: 790, Y: 150})
	if w := st.Width(o); w != 500 {
		t.Errorf("dragged past its Max, it's %v wide, want 500", w)
	}
	h.click(500, 150)
	h.click(500, 150)
	if w := st.Width(o); w != 300 {
		t.Errorf("double-clicked, it's %v wide, want its Default 300", w)
	}
	if !strings.Contains(h.u.AccessTree().Dump(), "Resize the list") {
		t.Error("the splitter isn't named for assistive technology")
	}
}

// A width kept from a wider window is held to the Max of a narrower one, so the box after it keeps its room.
func TestSplitterKeepsToAMaxThatShrinks(t *testing.T) {
	st := &SplitState{Size: 480}
	if w := st.Width(SplitterOpts{Default: 300, Min: 200, Max: 400}); w != 400 {
		t.Errorf("kept at 480 under a Max of 400, it's %v wide", w)
	}
	if w := st.Width(SplitterOpts{Default: 300, Min: 200, Max: 150}); w != 200 {
		t.Errorf("a Max under the Min gives %v, want the Min", w)
	}
}
