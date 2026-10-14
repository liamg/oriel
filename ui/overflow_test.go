package ui

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
)

// laidOut builds one frame and returns the drawn text runs and the UI.
func laidOut(t *testing.T, build func(c *Ctx)) (*UI, map[string]render.Op) {
	t.Helper()
	u := newUI()
	var l render.List
	u.Frame(l.Record(1), render.Point{X: 800, Y: 600}, build)
	runs := map[string]render.Op{}
	for _, op := range l.Ops {
		if op.Kind == render.OpGlyphs {
			runs[op.Run.Text] = op
		}
	}
	return u, runs
}

const long = "a label far longer than the box it is drawn in"

// Text never draws outside its box: a row too narrow for its text shrinks the text to fit, cut with "…"; a fixed
// size beside it keeps its size; a text that fits is left alone.
func TestTextFitsItsRow(t *testing.T) {
	var row, icon, label ID
	u, runs := laidOut(t, func(c *Ctx) {
		row = c.Row("row", Style{W: Px(120), Pad: Pad(4), Gap: 4}, func(c *Ctx) {
			icon = c.Box("icon", Style{W: Px(20), H: Px(10)}, nil)
			label = c.Text("t", long, TextStyle{})
		})
		c.Text("short", "fits", TextStyle{})
	})
	r := func(id ID) render.Rect { x, _ := u.Rect(id); return x }
	if r(icon).Dx() != 20 {
		t.Errorf("the fixed box shrank to %v", r(icon).Dx())
	}
	if got := r(label); got.Max.X > r(row).Max.X-4 {
		t.Errorf("the text ends at %v, past its row's inner edge %v", got.Max.X, r(row).Max.X-4)
	}
	var cut string
	for s := range runs {
		if strings.HasSuffix(s, "…") {
			cut = s
		}
	}
	if cut == "" || !strings.HasPrefix(long, strings.TrimSuffix(cut, "…")) {
		t.Errorf("the text wasn't cut with an ellipsis: %v", runs)
	}
	if _, ok := runs["fits"]; !ok {
		t.Error("a text that fits was changed")
	}
}

// A column too narrow for a text clamps it; a box between them shrinks with it.
func TestTextFitsItsColumnThroughABox(t *testing.T) {
	var col, label ID
	u, runs := laidOut(t, func(c *Ctx) {
		col = c.Column("col", Style{W: Px(150), Pad: Pad(5)}, func(c *Ctx) {
			c.Row("chip", Style{Pad: PadXY(6, 2)}, func(c *Ctx) {
				label = c.Text("t", long, TextStyle{})
			})
		})
	})
	cr, _ := u.Rect(col)
	lr, _ := u.Rect(label)
	if lr.Max.X > cr.Max.X-5-6 {
		t.Errorf("the text ends at %v, past the chip inside its column (%v)", lr.Max.X, cr.Max.X-11)
	}
	if _, whole := runs[long]; whole {
		t.Error("the whole text was drawn")
	}
}

// A box that scrolls keeps its content whole: that's what the scrolling is for.
func TestScrollingKeepsTextWhole(t *testing.T) {
	_, runs := laidOut(t, func(c *Ctx) {
		c.Row("row", Style{W: Px(120), ScrollX: true}, func(c *Ctx) {
			c.Text("t", long, TextStyle{})
		})
	})
	if _, whole := runs[long]; !whole {
		t.Error("text in a scrolling box was cut")
	}
}

// A stretchy child is never narrower than its content while there's room for it, so its text isn't cut.
func TestGrowChildKeepsItsContent(t *testing.T) {
	_, runs := laidOut(t, func(c *Ctx) {
		c.Row("row", Style{}, func(c *Ctx) {
			c.Box("a", Style{W: Flex(1)}, func(c *Ctx) { c.Text("t", long, TextStyle{}) })
			c.Box("b", Style{W: Flex(1)}, func(c *Ctx) { c.Text("t", "b", TextStyle{}) })
		})
	})
	if _, whole := runs[long]; !whole {
		t.Errorf("a stretchy box's text was cut with room to spare: %v", runs)
	}
}

// In a row too narrow, content gives way before controls (tabs, buttons) and what's marked Keep, which keep their labels
// whole; the last content gives way first.
func TestContentGivesWayBeforeControls(t *testing.T) {
	_, runs := laidOut(t, func(c *Ctx) {
		c.Row("row", Style{W: Px(200)}, func(c *Ctx) {
			c.Row("tabs", Style{}, func(c *Ctx) {
				c.Row("tab", Style{Pad: PadXY(6, 2), Interactive: true}, func(c *Ctx) { c.Text("t", "SESSION", TextStyle{}) })
			})
			c.Row("chip", Style{Keep: true}, func(c *Ctx) { c.Text("t", "WORKING", TextStyle{}) })
			c.Text("n", "5", TextStyle{})
			c.Row("line", Style{}, func(c *Ctx) { c.Text("t", long, TextStyle{}) })
			c.Text("note", "a note", TextStyle{})
		})
	})
	for _, whole := range []string{"SESSION", "WORKING", "5"} {
		if _, ok := runs[whole]; !ok {
			t.Errorf("%q was cut before the wider text beside it: %v", whole, runs)
		}
	}
	if _, whole := runs[long]; whole {
		t.Error("the text wasn't cut")
	}
	if _, whole := runs["a note"]; whole {
		t.Error("the note at the end was kept while the text before it was cut")
	}
}

// Overflowing finds a text past its box: here a fixed box wider than the one it's in.
func TestOverflowingFindsText(t *testing.T) {
	u, _ := laidOut(t, func(c *Ctx) {
		c.Row("row", Style{W: Px(100)}, func(c *Ctx) {
			c.Box("wide", Style{W: Px(400)}, func(c *Ctx) { c.Text("t", long, TextStyle{}) })
		})
		c.Row("fits", Style{W: Px(100)}, func(c *Ctx) { c.Text("t", long, TextStyle{}) })
		c.Row("scrolls", Style{W: Px(100), ScrollX: true}, func(c *Ctx) { c.Text("t", long, TextStyle{}) })
	})
	got := u.Overflowing()
	if len(got) != 1 || !strings.Contains(got[0], "past row") {
		t.Errorf("Overflowing = %q, want the text in the fixed box past row", got)
	}
}
