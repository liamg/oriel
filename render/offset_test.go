package render

import "testing"

// Offset draws something made for the origin anywhere: every coordinate moves by d, nothing else changes.
func TestOffset(t *testing.T) {
	var l List
	f := Offset(l.Record(2), Point{X: 100, Y: 50})
	red := Color{R: 255, A: 255}
	f.Rect(XYWH(1, 2, 3, 4), Style{Fill: red})
	f.Path(new(Path).MoveTo(Point{X: 1, Y: 1}).QuadTo(Point{X: 2, Y: 2}, Point{X: 3, Y: 1}), Style{Fill: red})
	f.Glyphs(nil, Point{X: 5, Y: 6}, red)
	f.Image(nil, Point{X: 7, Y: 8})
	f.Effect(Glow, XYWH(0, 0, 1, 1), 4, red)
	f.PushClip(XYWH(0, 0, 10, 10), 3)
	f.Pop()
	if f.Scale() != 2 {
		t.Errorf("scale %v", f.Scale())
	}
	want := []Point{{101, 52}, {101, 51}, {105, 56}, {107, 58}, {100, 50}, {100, 50}}
	got := []Point{l.Ops[0].Rect.Min, l.Ops[1].Path.Segs[0].Pts[0], l.Ops[2].At, l.Ops[3].At, l.Ops[4].Rect.Min, l.Ops[5].Rect.Min}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("op %d at %v, want %v", i, got[i], want[i])
		}
	}
	if q := l.Ops[1].Path.Segs[1].Pts; q[0] != (Point{102, 52}) || q[1] != (Point{103, 51}) {
		t.Errorf("quad %v", q)
	}
}
