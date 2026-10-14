package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// ui.md, "Annotation house style (explain, tours, :Inspect): numbered diamond, angled leader line, clipped-corner
// card".
func TestAnnotation(t *testing.T) {
	var id ui.ID
	var ground render.Color
	h := newHarness(t, theme.Dark, render.Point{X: 640, Y: 300}, func(c *ui.Ctx) {
		ground = col(c, theme.BgBase)
		c.Column("col", ui.Style{Pad: ui.Pad(20)}, func(c *ui.Ctx) {
			id = Annotation(c, "note", AnnotationOpts{N: "1", Title: "Rounded once, at creation", Tone: col(c, theme.StatusWorking),
				Width: ui.Px(440), Help: "test/note"}, func(c *ui.Ctx) {
				c.Text("p", "A line is rounded when it's priced, never later.", AnnotationBody(c))
			})
		})
	})
	h.frame()
	for _, s := range []string{"1", "ROUNDED ONCE, AT CREATION", "A line is rounded when it's priced, never later."} {
		if !h.drew(s) {
			t.Errorf("didn't draw %q: %q", s, h.texts())
		}
	}
	if n := h.access(id); n == nil || n.Name != "Rounded once, at creation" {
		t.Errorf("its accessible node: %+v", n)
	}
	r := h.rect(id)
	if r.Dx() != 440 {
		t.Errorf("width %v, want 440", r.Dx())
	}
	// The cut corners: two triangles in the ground's colour at the card's top right and bottom left, and a diamond.
	var cuts []render.Rect
	diamonds := 0
	for _, op := range h.list.Ops {
		if op.Kind != render.OpPath {
			continue
		}
		b := pathBounds(op.Path)
		if op.Style.Fill == ground && b.Dx() == annotationCut {
			cuts = append(cuts, b)
		}
		if len(op.Path.Segs) == 5 && b.Dx() == 18 {
			diamonds++
		}
	}
	if len(cuts) != 2 || cuts[0].Max.X != r.Max.X || cuts[0].Min.Y != r.Min.Y || cuts[1].Min.X != r.Min.X || cuts[1].Max.Y != r.Max.Y {
		t.Errorf("cut corners %v of a card at %v", cuts, r)
	}
	if diamonds != 1 {
		t.Errorf("%d numbered diamonds, want 1", diamonds)
	}
}

// A leader runs level from the code, angles to the card's height over 40 px, then runs level into it.
func TestLeaderPath(t *testing.T) {
	p := LeaderPath(render.Point{X: 760, Y: 98}, render.Point{X: 930, Y: 70})
	var pts []render.Point
	for _, s := range p.Segs {
		pts = append(pts, s.Pts[0])
	}
	want := []render.Point{{X: 760, Y: 98}, {X: 820, Y: 98}, {X: 860, Y: 70}, {X: 930, Y: 70}}
	if len(pts) != len(want) {
		t.Fatalf("points %v, want %v", pts, want)
	}
	for i := range want {
		if pts[i] != want[i] {
			t.Errorf("points %v, want %v (07-explain's M760 98 H820 L860 70 H930)", pts, want)
			break
		}
	}
}

func pathBounds(p *render.Path) render.Rect {
	var r render.Rect
	for i, s := range p.Segs {
		if s.Op == render.Close {
			continue
		}
		pt := s.Pts[0]
		if i == 0 {
			r = render.Rect{Min: pt, Max: pt}
		}
		r.Min.X, r.Min.Y = min(r.Min.X, pt.X), min(r.Min.Y, pt.Y)
		r.Max.X, r.Max.Y = max(r.Max.X, pt.X), max(r.Max.Y, pt.Y)
	}
	return r
}
