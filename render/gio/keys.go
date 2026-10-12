package gio

import (
	"strings"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"

	"github.com/liamg/oriel/render"
)

// names maps Gio's key names to vim's key notation, without the <>.
var names = map[key.Name]string{
	key.NameLeftArrow: "Left", key.NameRightArrow: "Right", key.NameUpArrow: "Up", key.NameDownArrow: "Down",
	key.NameReturn: "CR", key.NameEnter: "CR", key.NameEscape: "Esc", key.NameHome: "Home", key.NameEnd: "End",
	key.NameDeleteBackward: "BS", key.NameDeleteForward: "Del", key.NamePageUp: "PageUp", key.NamePageDown: "PageDown",
	key.NameTab: "Tab", key.NameSpace: " ",
	key.NameF1: "F1", key.NameF2: "F2", key.NameF3: "F3", key.NameF4: "F4", key.NameF5: "F5", key.NameF6: "F6",
	key.NameF7: "F7", key.NameF8: "F8", key.NameF9: "F9", key.NameF10: "F10", key.NameF11: "F11", key.NameF12: "F12",
}

// translate turns Gio input into key events. Typed text comes from EditEvents, so a key press
// that only types a character is left to its EditEvent; presses with Ctrl, Alt or Super, and named
// keys, come from key.Events.
func translate(ev event.Event) []render.Event {
	switch ev := ev.(type) {
	case key.EditEvent:
		var out []render.Event
		for _, r := range ev.Text {
			out = append(out, render.KeyEvent{Key: string(r)})
		}
		return out
	case key.Event:
		if ev.State != key.Press {
			return nil
		}
		var mods render.Mods
		if ev.Modifiers.Contain(key.ModShift) {
			mods |= render.ModShift
		}
		if ev.Modifiers.Contain(key.ModCtrl) {
			mods |= render.ModCtrl
		}
		if ev.Modifiers.Contain(key.ModAlt) {
			mods |= render.ModAlt
		}
		if ev.Modifiers.Contain(key.ModSuper) || ev.Modifiers.Contain(key.ModCommand) {
			mods |= render.ModSuper
		}
		name, named := names[ev.Name]
		if !named {
			name = string(ev.Name)
			if utf8.RuneCountInString(name) != 1 {
				return nil
			}
			if mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == 0 {
				return nil // typed text, which arrives as an EditEvent
			}
			if mods&render.ModShift == 0 {
				name = strings.ToLower(name)
			}
		} else if name == " " && mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == 0 {
			return nil
		}
		return []render.Event{render.KeyEvent{Key: name, Mods: mods}}
	}
	return nil
}
