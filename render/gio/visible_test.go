package gio

import (
	"testing"

	"gioui.org/app"

	"github.com/liamg/oriel/render"
)

// The window is hidden while it's minimised or covered, told once per change.
func TestVisibilityChangesAreEvents(t *testing.T) {
	var out []render.Event
	v := &visibility{tell: func(hidden bool) { out = append(out, render.VisibleEvent{Visible: !hidden}) }}
	v.config(app.ConfigEvent{Config: app.Config{Mode: app.Windowed}})
	v.config(app.ConfigEvent{Config: app.Config{Mode: app.Minimized}})
	v.occlude(true)
	v.config(app.ConfigEvent{Config: app.Config{Mode: app.Windowed}})
	v.config(app.ConfigEvent{Config: app.Config{Mode: app.Maximized}})
	v.occlude(false)
	v.occlude(false)
	want := []render.Event{render.VisibleEvent{Visible: false}, render.VisibleEvent{Visible: true}}
	if len(out) != len(want) || out[0] != want[0] || out[1] != want[1] {
		t.Fatalf("events %v, want %v", out, want)
	}
	if v.hidden() {
		t.Fatal("still hidden")
	}
}
