package gio

import (
	"image"
	"reflect"
	"testing"

	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/op"

	"github.com/liamg/oriel/render"
)

// The window's key filters let through the keys text fields edit with, as gio's router delivers them: ⇧⏎ (a new
// line in a message), ⏎, ⌘A and Ctrl+A (select all), and Tab, which gio keeps for its focus moves unless named.
func TestKeyFiltersDeliver(t *testing.T) {
	tag := new(int)
	r := new(input.Router)
	filters := []event.Filter{key.FocusFilter{Target: tag}, keyFilter(tag), tabFilter(tag)}
	drain := func() (out []render.Event) {
		for {
			ev, ok := r.Event(filters...)
			if !ok {
				return out
			}
			out = append(out, translate(ev, 1, nil, new(render.Mods))...)
		}
	}
	frame := func() {
		ops := new(op.Ops)
		inputArea(ops, image.Pt(100, 100), tag, render.CursorDefault)
		r.Frame(ops)
	}
	drain()
	r.Source().Execute(key.FocusCmd{Tag: tag})
	frame()
	drain()
	for _, tt := range []struct {
		in   event.Event
		want render.KeyEvent
	}{
		{key.Event{Name: key.NameReturn, Modifiers: key.ModShift, State: key.Press}, render.KeyEvent{Key: "CR", Mods: render.ModShift}},
		{key.Event{Name: key.NameReturn, State: key.Press}, render.KeyEvent{Key: "CR"}},
		{key.Event{Name: "A", Modifiers: key.ModCommand, State: key.Press}, render.KeyEvent{Key: "a", Mods: render.ModSuper}},
		{key.Event{Name: "A", Modifiers: key.ModCtrl, State: key.Press}, render.KeyEvent{Key: "a", Mods: render.ModCtrl}},
		{input.SystemEvent{Event: key.Event{Name: key.NameTab, State: key.Press}}, render.KeyEvent{Key: "Tab"}},
		{input.SystemEvent{Event: key.Event{Name: key.NameTab, Modifiers: key.ModShift, State: key.Press}}, render.KeyEvent{Key: "Tab", Mods: render.ModShift}},
	} {
		r.Queue(tt.in)
		if got := drain(); !reflect.DeepEqual(got, []render.Event{tt.want}) {
			t.Errorf("%+v arrives as %+v, want %+v", tt.in, got, tt.want)
		}
	}
}
