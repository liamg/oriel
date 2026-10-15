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
		if got := translate(tt.in, 2, new(pointer.Buttons), new(render.Mods)); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("translate(%+v) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

// SH-8: a modifier going down or up is the set held now, so ⌥ held alone can pull the HUD down; losing focus lets go of
// them all.
func TestTranslateMods(t *testing.T) {
	var down render.Mods
	for _, tt := range []struct {
		in   event.Event
		want []render.Event
	}{
		{key.Event{Name: key.NameAlt, State: key.Press}, []render.Event{render.ModsEvent{Mods: render.ModAlt}}},
		{key.Event{Name: key.NameAlt, State: key.Press, Modifiers: key.ModAlt}, nil}, // a repeat
		{key.Event{Name: key.NameCommand, State: key.Press}, []render.Event{render.ModsEvent{Mods: render.ModAlt | render.ModSuper}}},
		{key.Event{Name: key.NameAlt, State: key.Release}, []render.Event{render.ModsEvent{Mods: render.ModSuper}}},
		{key.FocusEvent{Focus: false}, []render.Event{render.ModsEvent{}}},
		{key.FocusEvent{Focus: false}, nil},
		{key.Event{Name: key.NameShift, State: key.Press}, []render.Event{render.ModsEvent{Mods: render.ModShift}}},
	} {
		if got := translate(tt.in, 1, new(pointer.Buttons), &down); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("translate(%+v) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

// Gio's release carries the buttons still held, as macOS and X11 report them; ours carries the one released.
func TestTranslateClick(t *testing.T) {
	var held pointer.Buttons
	for _, tt := range []struct {
		in   pointer.Event
		want render.Buttons
	}{
		{pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary}, render.ButtonPrimary},
		{pointer.Event{Kind: pointer.Press, Buttons: pointer.ButtonPrimary | pointer.ButtonSecondary}, render.ButtonSecondary},
		{pointer.Event{Kind: pointer.Drag, Buttons: pointer.ButtonPrimary | pointer.ButtonSecondary}, render.ButtonPrimary | render.ButtonSecondary},
		{pointer.Event{Kind: pointer.Release, Buttons: pointer.ButtonPrimary}, render.ButtonSecondary},
		{pointer.Event{Kind: pointer.Release}, render.ButtonPrimary},
	} {
		got := translate(tt.in, 1, &held, new(render.Mods))
		if len(got) != 1 || got[0].(render.PointerEvent).Buttons != tt.want {
			t.Errorf("translate(%v %v) = %+v, want buttons %v", tt.in.Kind, tt.in.Buttons, got, tt.want)
		}
	}
}
