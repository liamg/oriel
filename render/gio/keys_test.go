package gio

import (
	"reflect"
	"testing"

	"gioui.org/io/event"
	"gioui.org/io/key"

	"github.com/liamg/oriel/render"
)

func TestTranslate(t *testing.T) {
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
	} {
		if got := translate(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("translate(%+v) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}
