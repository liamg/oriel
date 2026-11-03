//go:build darwin && !ios && cgo

package gio

// The exports are in a file of their own: cgo allows only declarations in the preamble of a file with one.

/*
#include <stdlib.h>
#include "access_darwin.h"
*/
import "C"

import (
	"unicode/utf16"

	"github.com/liamg/oriel/render"
)

//export orielAXInfoOf
func orielAXInfoOf(h C.uintptr_t, id C.uint64_t, text C.int, out *C.orielAXInfo) C.int {
	b := appkitBridgeOf(h)
	if b == nil {
		return 0
	}
	e, ok := b.ax.tree.get(uint64(id))
	if !ok {
		return 0
	}
	n := &e.node
	role, sub := appkitRoleOf(n.Role)
	kind, value, num := appkitValue(n)
	flag := func(on bool) C.int {
		if on {
			return 1
		}
		return 0
	}
	out.role, out.subrole = C.int(role), C.int(sub)
	out.valueKind, out.number = C.int(kind), C.int(num)
	out.enabled, out.focused, out.selected = flag(!n.Disabled), flag(n.Focused), flag(n.Selected)
	out.hasExpanded, out.expanded = flag(n.Expanded != nil), flag(n.Expanded != nil && *n.Expanded)
	out.press = flag(has(n, render.ActionClick))
	out.step = flag(has(n, render.ActionIncrement) || has(n, render.ActionDecrement))
	out.text = flag(n.Role == render.RoleTextBox)
	pt := b.points()
	out.x, out.y = C.double(n.Bounds.Min.X*pt), C.double(n.Bounds.Min.Y*pt)
	out.w, out.h = C.double(n.Bounds.Dx()*pt), C.double(n.Bounds.Dy()*pt)
	if text != 0 {
		out.label, out.value = C.CString(appkitLabel(n)), C.CString(value)
	}
	if id != 0 {
		out.parent = C.uint64_t(e.parent)
	}
	out.chars = C.long(len(utf16.Encode([]rune(n.Value))))
	sel := n.Selection
	if sel.End < sel.Start {
		sel.Start, sel.End = sel.End, sel.Start
	}
	loc, l := toUTF16(n.Value, sel)
	out.selLoc, out.selLen = C.long(loc), C.long(l)
	return 1
}

//export orielAXKids
func orielAXKids(h C.uintptr_t, id C.uint64_t, n *C.int) *C.uintptr_t {
	*n = 0
	b := appkitBridgeOf(h)
	if b == nil {
		return nil
	}
	b.on.Store(true)
	e, ok := b.ax.tree.get(uint64(id))
	if !ok {
		return nil
	}
	hs := make([]uintptr, 0, len(e.kids))
	for _, k := range e.kids {
		hs = append(hs, b.els.element(k))
	}
	*n = C.int(len(hs))
	return cArray(hs)
}

//export orielAXElementOf
func orielAXElementOf(h C.uintptr_t, id C.uint64_t) C.uintptr_t {
	b := appkitBridgeOf(h)
	if b == nil || id == 0 {
		return 0
	}
	if _, ok := b.ax.tree.get(uint64(id)); !ok {
		return 0
	}
	return C.uintptr_t(b.els.element(uint64(id)))
}

//export orielAXHit
func orielAXHit(h C.uintptr_t, x, y C.double) C.uintptr_t {
	b := appkitBridgeOf(h)
	if b == nil {
		return 0
	}
	pt := b.points()
	return orielAXElementOf(h, C.uint64_t(b.ax.tree.hit(render.Point{X: float32(x) / pt, Y: float32(y) / pt})))
}

//export orielAXFocusedElement
func orielAXFocusedElement(h C.uintptr_t) C.uintptr_t {
	b := appkitBridgeOf(h)
	if b == nil {
		return 0
	}
	return orielAXElementOf(h, C.uint64_t(b.ax.tree.focused()))
}

//export orielAXDo
func orielAXDo(h C.uintptr_t, id C.uint64_t, action C.int, text *C.char, loc, n C.long) C.int {
	b := appkitBridgeOf(h)
	if b == nil {
		return 0
	}
	e, ok := b.ax.tree.get(uint64(id))
	if !ok {
		return 0
	}
	x := render.AccessAction{ID: uint64(id)}
	switch action {
	case C.orielAXPress:
		x.Action = render.ActionClick
	case C.orielAXIncrement:
		x.Action = render.ActionIncrement
	case C.orielAXDecrement:
		x.Action = render.ActionDecrement
	case C.orielAXFocus:
		x.Action = render.ActionFocus
	case C.orielAXSetValue:
		x.Action, x.Value = render.ActionSetValue, C.GoString(text)
	case C.orielAXSelect:
		x.Action, x.Selection = render.ActionSelect, fromUTF16(e.node.Value, int(loc), int(n))
	default:
		return 0
	}
	if b.ax.act(x) {
		return 1
	}
	return 0
}

//export orielAXSubstring
func orielAXSubstring(h C.uintptr_t, id C.uint64_t, loc, n C.long) *C.char {
	b := appkitBridgeOf(h)
	if b == nil {
		return nil
	}
	e, ok := b.ax.tree.get(uint64(id))
	if !ok {
		return nil
	}
	r := fromUTF16(e.node.Value, int(loc), int(n))
	return C.CString(string([]rune(e.node.Value)[r.Start:r.End]))
}
