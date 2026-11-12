package gio

import "testing"

// A click on Oriel's window while another app has the focus (a picture-in-picture video that keeps taking it back)
// does what it's on as well as bringing the window forward: AppKit's acceptsFirstMouse: defaults to NO, which spends
// that first click on the focus alone, so every control needed two.
func TestTheViewTakesTheFirstClick(t *testing.T) {
	if !viewTakesFirstClick() {
		t.Error("Oriel's view refuses the click that brings its window forward")
	}
}
