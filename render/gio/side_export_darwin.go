//go:build darwin && !ios

package gio

// The export is in a file of its own: cgo allows only declarations in the preamble of a file with one.

/*
#include <stdint.h>
*/
import "C"

import "github.com/liamg/oriel/render"

//export orielSideButton
func orielSideButton(h C.uintptr_t, button C.int, flags C.ulong) {
	fn, ok := sideWindows.Load(uintptr(h))
	if !ok {
		return
	}
	var m render.Mods
	for bit, mod := range map[C.ulong]render.Mods{1 << 17: render.ModShift, 1 << 18: render.ModCtrl, 1 << 19: render.ModAlt, 1 << 20: render.ModSuper} {
		if flags&bit != 0 {
			m |= mod
		}
	}
	k := SideBack
	if button == 4 {
		k = SideForward
	}
	fn.(func(render.Event))(render.KeyEvent{Key: k, Mods: m})
}
