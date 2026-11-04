package render

import (
	"testing"

	"github.com/liamg/oriel/render/typeset"
)

// Runs says what of each run of text shows: a clip opening over it shows only what's inside, nested clips the part
// inside both, a fully transparent layer none of it.
func TestRunsShowWhatTheirClipsLetThrough(t *testing.T) {
	var l List
	f := l.Record(1)
	run := func(text string) *typeset.Run { return &typeset.Run{Text: text, Advance: 100, Ascent: 10, Descent: 4} }
	f.Glyphs(run("free"), Point{X: 0, Y: 20}, Color{A: 255})
	f.PushClip(XYWH(0, 0, 60, 800), 0)
	f.Glyphs(run("half"), Point{X: 10, Y: 40}, Color{A: 255})
	f.PushClip(XYWH(30, 0, 500, 800), 0)
	f.Glyphs(run("both"), Point{X: 10, Y: 60}, Color{A: 255})
	f.Pop()
	f.Glyphs(run("past"), Point{X: 200, Y: 80}, Color{A: 255})
	f.Pop()
	f.PushOpacity(0)
	f.Glyphs(run("faded"), Point{X: 0, Y: 100}, Color{A: 255})
	f.Pop()
	want := map[string]struct {
		shown   Rect
		visible bool
	}{
		"free":  {XYWH(0, 10, 100, 14), true},
		"half":  {Rect{Point{10, 30}, Point{60, 44}}, true},
		"both":  {Rect{Point{30, 50}, Point{60, 64}}, true},
		"past":  {Rect{Point{200, 70}, Point{200, 70}}, false},
		"faded": {XYWH(0, 90, 100, 14), false},
	}
	runs := l.Runs()
	if len(runs) != len(want) {
		t.Fatalf("%d runs, want %d: %+v", len(runs), len(want), runs)
	}
	for _, r := range runs {
		w := want[r.Text]
		if r.Shown != w.shown || r.Visible() != w.visible {
			t.Errorf("%s: shown %v visible %v, want %v %v", r.Text, r.Shown, r.Visible(), w.shown, w.visible)
		}
	}
}
