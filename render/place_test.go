package render

import "testing"

func TestPlaceWindow(t *testing.T) {
	laptop := XYWH(0, 25, 1440, 875)     // a 13" Mac's visible frame, under the menu bar
	monitor := XYWH(1440, 0, 2560, 1415) // a monitor to its right
	small := XYWH(0, 0, 1000, 700)
	def := Point{X: 1280, Y: 800}
	for _, tt := range []struct {
		name    string
		screens []Rect
		saved   Rect
		want    Rect
	}{
		{"first launch: the default, centred", []Rect{laptop}, Rect{}, XYWH(80, 68.75, 1280, 787.5)},
		{"first launch on a small screen: 90% of it", []Rect{small}, Rect{}, XYWH(50, 35, 900, 630)},
		{"where it was", []Rect{laptop, monitor}, XYWH(1600, 100, 1400, 900), XYWH(1600, 100, 1400, 900)},
		{"half off the right edge: moved in", []Rect{laptop}, XYWH(900, 100, 1000, 600), XYWH(440, 100, 1000, 600)},
		{"above the menu bar: moved down", []Rect{laptop}, XYWH(100, -40, 800, 600), XYWH(100, 25, 800, 600)},
		{"bigger than its screen: shrunk to it", []Rect{laptop}, XYWH(0, 25, 2000, 1200), laptop},
		{"mostly on the monitor: kept there", []Rect{laptop, monitor}, XYWH(1300, 100, 1000, 600), XYWH(1440, 100, 1000, 600)},
		{"on a monitor that's gone: the first screen, centred", []Rect{laptop}, XYWH(3000, 200, 1000, 600), XYWH(220, 162.5, 1000, 600)},
		{"no screens known: as asked", nil, Rect{}, XYWH(0, 0, 1280, 800)},
		{"no screens known, saved: as saved", nil, XYWH(10, 20, 900, 700), XYWH(10, 20, 900, 700)},
	} {
		if got := PlaceWindow(tt.screens, tt.saved, def); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
}
