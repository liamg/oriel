package gio

import (
	"strings"
	"unicode/utf8"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"

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

// modKeys are the modifier keys by Gio's name.
var modKeys = map[key.Name]render.Mods{key.NameShift: render.ModShift, key.NameCtrl: render.ModCtrl, key.NameAlt: render.ModAlt,
	key.NameSuper: render.ModSuper, key.NameCommand: render.ModSuper}

// translate turns Gio input into key events. Typed text comes from EditEvents, so a key press
// that only types a character is left to its EditEvent; presses with Ctrl, Alt or Super, and named
// keys, come from key.Events. held is the buttons down before ev: Gio's press and release carry those
// down after it, and ours the one pressed or released. down is the modifier keys held: a modifier's press or release
// is a ModsEvent of them.
func translate(ev event.Event, scale float32, held *pointer.Buttons, down *render.Mods) []render.Event {
	switch ev := ev.(type) {
	case pointer.Event:
		pe := render.PointerEvent{Pos: render.Point{X: ev.Position.X / scale, Y: ev.Position.Y / scale}, Mods: mods(ev.Modifiers)}
		now := ev.Buttons & (pointer.ButtonPrimary | pointer.ButtonSecondary | pointer.ButtonTertiary)
		pe.Buttons = render.Buttons(now)
		switch ev.Kind {
		case pointer.Move, pointer.Drag:
			pe.Kind = render.PointerMove
		case pointer.Press:
			pe.Kind = render.PointerPress
			if b := now &^ *held; b != 0 {
				pe.Buttons = render.Buttons(b)
			}
		case pointer.Release:
			pe.Kind = render.PointerRelease
			pe.Buttons = render.Buttons(*held &^ now)
		case pointer.Scroll:
			pe.Kind = render.PointerScroll
			pe.Scroll = render.Point{X: ev.Scroll.X / scale, Y: ev.Scroll.Y / scale}
		case pointer.Leave:
			pe.Kind = render.PointerLeave
		default:
			return nil
		}
		if ev.Kind != pointer.Scroll {
			*held = now
		}
		return []render.Event{pe}
	case key.EditEvent:
		var out []render.Event
		for _, r := range ev.Text {
			out = append(out, render.KeyEvent{Key: string(r)})
		}
		return out
	case key.FocusEvent:
		if ev.Focus || *down == 0 {
			return nil
		}
		*down = 0
		return []render.Event{render.ModsEvent{}}
	case key.Event:
		if m, ok := modKeys[ev.Name]; ok {
			was := *down
			if ev.State == key.Press {
				*down |= m
			} else {
				*down &^= m
			}
			if *down == was {
				return nil
			}
			return []render.Event{render.ModsEvent{Mods: *down}}
		}
		if ev.State != key.Press {
			return nil
		}
		mods := mods(ev.Modifiers)
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

func mods(m key.Modifiers) render.Mods {
	var out render.Mods
	if m.Contain(key.ModShift) {
		out |= render.ModShift
	}
	if m.Contain(key.ModCtrl) {
		out |= render.ModCtrl
	}
	if m.Contain(key.ModAlt) {
		out |= render.ModAlt
	}
	if m.Contain(key.ModSuper) || m.Contain(key.ModCommand) {
		out |= render.ModSuper
	}
	return out
}
