package render

import (
	"testing"

	"github.com/liamg/oriel/render/typeset"
)

func record(fill Color, label string) *List {
	l := new(List)
	f := l.Record(2)
	f.Rect(Rect{Max: Point{X: 100, Y: 50}}, Style{Fill: fill, Gradient: &Gradient{From: fill}})
	p := &Path{}
	p.Segs = append(p.Segs, Seg{Op: MoveTo}, Seg{Op: LineTo})
	f.Path(p, Style{Stroke: fill, Width: 1})
	f.Glyphs(&typeset.Run{Text: label, Advance: 10}, Point{X: 4, Y: 4}, fill)
	return l
}

// A frame that draws exactly what the last one did is Same, though it was built again (new paths, new runs); any
// change to what's drawn isn't.
func TestListSame(t *testing.T) {
	red, blue := Color{R: 255, A: 255}, Color{B: 255, A: 255}
	if !record(red, "a").Same(record(red, "a")) {
		t.Error("the same frame built twice isn't Same")
	}
	for name, other := range map[string]*List{"a colour": record(blue, "a"), "a run's text": record(red, "b")} {
		if record(red, "a").Same(other) {
			t.Errorf("a frame with another %s is Same", name)
		}
	}
	if (&List{}).Same(nil) {
		t.Error("a frame is Same as none")
	}
}
