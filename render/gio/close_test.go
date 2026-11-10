package gio

import (
	"testing"

	"github.com/liamg/oriel/render"
)

// Once the platform says the window has closed, the app's events end, though Gio's loop hasn't (AppKit tells Gio
// only when the view is freed) and events are still coming.
func TestEventsEndWhenThePlatformClosesTheWindow(t *testing.T) {
	w := newStreams()
	go w.forward()
	w.post(render.RedrawEvent{})
	if _, ok := <-w.Events(); !ok {
		t.Fatal("the events ended before the window closed")
	}
	w.end()
	w.post(render.RedrawEvent{})
	for range w.Events() {
	}
	w.end()
	close(w.queue)
}
