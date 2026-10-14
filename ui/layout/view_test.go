package layout

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var screen = render.Point{X: 1200, Y: 800}

type harness struct {
	t     *testing.T
	u     *ui.UI
	r     *Root
	panes Panes
	list  *render.List
	now   time.Time
	held  bool // the primary button is down, for moves
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	u := ui.New(ts, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}})
	u.SetTheme(th)
	u.SetOptions(func() ui.Options { o := u.Options(); o.ReducedMotion = true; return o }())
	h := &harness{t: t, u: u, r: start(t), now: time.Unix(1000, 0)}
	u.SetClock(func() time.Time { return h.now })
	h.frame()
	h.frame()
	return h
}

func (h *harness) frame() {
	h.list = new(render.List)
	h.u.Frame(h.list.Record(1), screen, func(c *ui.Ctx) {
		View(c, "layout", h.r, &h.panes, func(c *ui.Ctx, v ViewRef, focused bool) {
			c.Text("name", v.String(), ui.TextStyle{})
		})
	})
}

func (h *harness) ptr(k render.PointerKind, p render.Point) {
	b := render.Buttons(0)
	if k == render.PointerPress || k == render.PointerRelease || k == render.PointerMove && h.held {
		b = render.ButtonPrimary
	}
	h.u.Pointer(render.PointerEvent{Kind: k, Pos: p, Buttons: b})
	h.frame()
}

// tabAt is the centre of v's tab, from the latest frame's display list.
func (h *harness) tabAt(v ViewRef) render.Point {
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == v.String() && op.At.Y < 1000 {
			// Tab labels sit in 30px strips; the content's copy of the name is lower down its pane.
			if row := int(op.At.Y); row%30 >= 10 && h.isTab(op) {
				return render.Point{X: op.At.X + 10, Y: op.At.Y - 4}
			}
		}
	}
	h.t.Fatalf("no tab for %s", v)
	return render.Point{}
}

func (h *harness) isTab(op render.Op) bool {
	return op.Run.Font.Size == 12 && op.Color != (render.Color{})
}

func TestRenderSnapshot(t *testing.T) {
	h := newHarness(t)
	snapshot.Golden(t, "testdata/view-start.list", []byte(snapshot.List(h.list)))
}

// Drag the terminal's tab over every drop target, pinning what lights up, then drop it on each.
func TestDragToEveryTarget(t *testing.T) {
	targets := []struct {
		name string
		at   func(main render.Rect) render.Point
	}{
		{"main, left edge", func(m render.Rect) render.Point { return render.Point{X: m.Min.X + m.Dx()*.1, Y: m.Min.Y + m.Dy()/2} }},
		{"main, right edge", func(m render.Rect) render.Point { return render.Point{X: m.Min.X + m.Dx()*.9, Y: m.Min.Y + m.Dy()/2} }},
		{"main, top edge", func(m render.Rect) render.Point { return render.Point{X: m.Min.X + m.Dx()/2, Y: m.Min.Y + m.Dy()*.1} }},
		{"main, bottom edge", func(m render.Rect) render.Point { return render.Point{X: m.Min.X + m.Dx()/2, Y: m.Min.Y + m.Dy()*.9} }},
		{"main, middle", func(m render.Rect) render.Point { return render.Point{X: m.Min.X + m.Dx()/2, Y: m.Min.Y + m.Dy()/2} }},
		{"the left window edge", func(render.Rect) render.Point { return render.Point{X: 5, Y: 300} }},
		{"the right window edge", func(render.Rect) render.Point { return render.Point{X: screen.X - 5, Y: 300} }},
		{"the bottom window edge", func(render.Rect) render.Point { return render.Point{X: 600, Y: screen.Y - 5} }},
		{"the left dock", func(render.Rect) render.Point { return render.Point{X: 120, Y: 300} }},
	}
	var b strings.Builder
	for _, tg := range targets {
		h := newHarness(t)
		main := h.panes.Rects(h.u)[h.r.Main]
		from := h.tabAt(term)
		h.held = true
		h.ptr(render.PointerMove, from)
		h.ptr(render.PointerPress, from)
		h.ptr(render.PointerMove, render.Point{X: from.X + 10, Y: from.Y - 10})
		to := tg.at(main)
		h.ptr(render.PointerMove, to)
		h.ptr(render.PointerMove, to)
		fmt.Fprintf(&b, "=== %s at %v,%v\n", tg.name, to.X, to.Y)
		for _, op := range h.list.Ops {
			if op.Kind == render.OpRect && op.Style.Width == 1.5 {
				fmt.Fprintf(&b, "lit %s\n", strings.TrimPrefix(snapshot.List(&render.List{Ops: []render.Op{op}}), "rect "))
			}
		}
		h.held = false
		h.ptr(render.PointerRelease, to)
		b.WriteString(h.r.Dump())
	}
	snapshot.Golden(t, "testdata/drag.golden", []byte(b.String()))
}

func TestClickTabAndContent(t *testing.T) {
	h := newHarness(t)
	h.ptr(render.PointerMove, h.tabAt(probs))
	p := h.tabAt(probs)
	h.ptr(render.PointerPress, p)
	h.ptr(render.PointerRelease, p)
	if w := h.r.Find(probs); w.Leaf.Tabs[w.Leaf.Active] != probs || h.r.Focus == nil || *h.r.Focus != probs {
		t.Errorf("clicking problems' tab: %s", h.r.Dump())
	}
}

func TestSplitterDrag(t *testing.T) {
	h := newHarness(t)
	h.r.Move(term, Split, Opts{Side: SideRight})
	h.frame()
	h.frame()
	rects := h.panes.Rects(h.u)
	var left render.Rect
	for l, rc := range rects {
		if len(l.Tabs) > 0 && l.Tabs[0] == code {
			left = rc
		}
	}
	x := left.Max.X
	h.held = true
	h.ptr(render.PointerMove, render.Point{X: x, Y: 300})
	h.ptr(render.PointerPress, render.Point{X: x, Y: 300})
	h.ptr(render.PointerMove, render.Point{X: x + 100, Y: 300})
	h.held = false
	h.ptr(render.PointerRelease, render.Point{X: x + 100, Y: 300})
	if w := h.r.Main.Weights; !(w[0] > w[1]) {
		t.Errorf("dragging the splitter right: weights %v", w)
	}
}

func TestFocusDir(t *testing.T) {
	h := newHarness(t)
	h.r.Move(term, Split, Opts{Side: SideRight})
	h.r.Focus = &code
	h.frame()
	h.frame()
	rects := h.panes.Rects(h.u)
	for _, step := range []struct {
		d    Dir
		want ViewRef
	}{{DirRight, term}, {DirLeft, code}, {DirLeft, tree}, {DirRight, code}, {DirDown, probs}, {DirUp, code}} {
		h.r.FocusDir(step.d, rects)
		if *h.r.Focus != step.want {
			t.Fatalf("⌃W %s: focus %s, want %s", step.d, h.r.Focus, step.want)
		}
	}
}

// A drag in progress, as drawn: the terminal's tab over main's right edge, which lights up.
func TestDragShot(t *testing.T) {
	h := newHarness(t)
	main := h.panes.Rects(h.u)[h.r.Main]
	from := h.tabAt(term)
	h.held = true
	h.ptr(render.PointerMove, from)
	h.ptr(render.PointerPress, from)
	h.ptr(render.PointerMove, render.Point{X: from.X + 10, Y: from.Y - 10})
	to := render.Point{X: main.Min.X + main.Dx()*.9, Y: main.Min.Y + main.Dy()/2}
	h.ptr(render.PointerMove, to)
	h.ptr(render.PointerMove, to)
	bg := h.u.Theme().C(theme.BgBase)
	snapshot.GoldenPNG(t, "testdata/drag-right.png", raster.Render(h.list, screen, 1, bg), 0.02, 0.001)
}

// FW-13: a dock slides shut and open from its edge, its content fading, on the motion spring: the start, a midpoint
// and the settled frame; with reduced motion it jumps.
func TestDockMotion(t *testing.T) {
	for _, reduced := range []bool{false, true} {
		h := newHarness(t)
		o := h.u.Options()
		o.ReducedMotion = reduced
		h.u.SetOptions(o)
		var b strings.Builder
		step := func(name string, d time.Duration) {
			for el := time.Duration(0); el < d; el += time.Second / 120 {
				h.now = h.now.Add(time.Second / 120)
				h.frame()
			}
			if d == 0 {
				h.frame()
			}
			fmt.Fprintf(&b, "== %s\n%s", name, snapshot.List(h.list))
		}
		for _, open := range []bool{false, true} {
			h.r.Docks[Bottom].Open = open
			step(fmt.Sprintf("open=%v start", open), 0)
			step(fmt.Sprintf("open=%v mid", open), 60*time.Millisecond)
			step(fmt.Sprintf("open=%v settled", open), 250*time.Millisecond)
		}
		name := "testdata/dock-motion.list"
		if reduced {
			name = "testdata/dock-motion-reduced.list"
		}
		snapshot.Golden(t, name, []byte(b.String()))
		if !h.u.NextFrame().IsZero() && h.u.NextFrame().Equal(h.now) {
			t.Error("the dock is still moving after 310 ms")
		}
	}
}

// A dock's inner edge is a handle: the resize cursor over it, and a drag changes the dock's size.
func TestDockEdgeDrag(t *testing.T) {
	h := newHarness(t)
	left, ok := h.dockRect(Left)
	bottom, ok2 := h.dockRect(Bottom)
	if !ok || !ok2 {
		t.Fatal("no left or bottom dock")
	}
	for _, d := range []struct {
		e      Edge
		at     render.Point
		by     render.Point
		cursor render.Cursor
		grows  float32
	}{
		{Left, render.Point{X: left.Max.X, Y: 300}, render.Point{X: 80}, render.CursorResizeCol, 80},
		{Bottom, render.Point{X: 600, Y: bottom.Min.Y}, render.Point{Y: -50}, render.CursorResizeRow, 50},
	} {
		before := h.r.Docks[d.e].Size
		h.ptr(render.PointerMove, d.at)
		if c := h.u.Cursor(); c != d.cursor {
			t.Errorf("%s edge: cursor %v, want %v", d.e, c, d.cursor)
		}
		h.held = true
		h.ptr(render.PointerPress, d.at)
		h.ptr(render.PointerMove, render.Point{X: d.at.X + d.by.X, Y: d.at.Y + d.by.Y})
		h.held = false
		h.ptr(render.PointerRelease, render.Point{X: d.at.X + d.by.X, Y: d.at.Y + d.by.Y})
		if got := h.r.Docks[d.e].Size - before; got != d.grows {
			t.Errorf("%s edge dragged: grew %v, want %v", d.e, got, d.grows)
		}
	}
}

// dockRect is where e's dock was drawn, found by its accessible name.
func (h *harness) dockRect(e Edge) (render.Rect, bool) {
	var find func(n *render.AccessNode) (render.Rect, bool)
	find = func(n *render.AccessNode) (render.Rect, bool) {
		if n.Name == dockNames[e] {
			return n.Bounds, true
		}
		for _, k := range n.Children {
			if r, ok := find(k); ok {
				return r, true
			}
		}
		return render.Rect{}, false
	}
	return find(h.u.AccessTree())
}

// SH-17: a view opened for a project names it on its tab ("api · tree"), and a tab Lead gives a colour leads with that
// colour's square and the tooltip Tip gives.
func TestTabProjectAndLead(t *testing.T) {
	h := newHarness(t)
	scoped := ViewRef{ID: "tree", Project: "api"}
	if err := h.r.Open(scoped, Docked, Opts{Edge: Left}); err != nil {
		t.Fatal(err)
	}
	square := render.Color{R: 0xa7, G: 0x8b, B: 0xfa, A: 0xff}
	h.panes.Lead = func(v ViewRef) (render.Color, bool) { return square, v == code }
	h.panes.Tip = func(v ViewRef) string {
		if v == code {
			return "api · proto/usage.proto"
		}
		return ""
	}
	h.frame()
	labelled, led := false, false
	var at render.Point
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "api · tree" {
			labelled = true
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == code.String() && h.isTab(op) && at == (render.Point{}) {
			at = render.Point{X: op.At.X + 10, Y: op.At.Y - 4}
		}
		if op.Kind == render.OpRect && op.Style.Fill == square {
			led = true
		}
	}
	if !labelled {
		t.Error("the scoped view's tab doesn't name its project")
	}
	if !led {
		t.Error("no lead square on the code tab")
	}
	h.ptr(render.PointerMove, at)
	h.now = h.now.Add(time.Second)
	h.frame()
	h.frame()
	tipped := false
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && strings.Contains(op.Run.Text, "proto/usage.proto") {
			tipped = true
		}
	}
	if !tipped {
		t.Errorf("hovering the tab at %v doesn't show its tooltip", at)
	}
}
