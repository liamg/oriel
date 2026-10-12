package gio

import (
	"reflect"
	"testing"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

	"github.com/liamg/oriel/render"
)

func TestTranslate(t *testing.T) {
	// Pointer positions arrive in device pixels; these are at 2×, except keys, which have none.
	for _, tt := range []struct {
		in   event.Event
		want []render.Event
	}{
		{key.EditEvent{Text: "aB"}, []render.Event{render.KeyEvent{Key: "a"}, render.KeyEvent{Key: "B"}}},
		{key.Event{Name: "A", State: key.Press}, nil},
		{key.Event{Name: "A", State: key.Press, Modifiers: key.ModShift}, nil},
		{key.Event{Name: "W", State: key.Press, Modifiers: key.ModCtrl}, []render.Event{render.KeyEvent{Key: "w", Mods: render.ModCtrl}}},
		{key.Event{Name: "S", State: key.Press, Modifiers: key.ModCommand}, []render.Event{render.KeyEvent{Key: "s", Mods: render.ModSuper}}},
		{key.Event{Name: key.NameEscape, State: key.Press}, []render.Event{render.KeyEvent{Key: "Esc"}}},
		{key.Event{Name: key.NameReturn, State: key.Press, Modifiers: key.ModShift}, []render.Event{render.KeyEvent{Key: "CR", Mods: render.ModShift}}},
		{key.Event{Name: key.NameSpace, State: key.Press}, nil},
		{key.Event{Name: key.NameSpace, State: key.Press, Modifiers: key.ModCtrl}, []render.Event{render.KeyEvent{Key: " ", Mods: render.ModCtrl}}},
		{key.Event{Name: key.NameEscape, State: key.Release}, nil},
		{pointer.Event{Kind: pointer.Press, Position: f32.Pt(20, 40), Buttons: pointer.ButtonPrimary}, []render.Event{render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 10, Y: 20}, Buttons: render.ButtonPrimary}}},
		{pointer.Event{Kind: pointer.Drag, Position: f32.Pt(4, 4), Buttons: pointer.ButtonPrimary}, []render.Event{render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 2, Y: 2}, Buttons: render.ButtonPrimary}}},
		{pointer.Event{Kind: pointer.Scroll, Scroll: f32.Pt(0, 30)}, []render.Event{render.PointerEvent{Kind: render.PointerScroll, Scroll: render.Point{Y: 15}}}},
	} {
		if got := translate(tt.in, 2); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("translate(%+v) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}
