package gio

import (
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// placer puts a window where it was (render.PlaceWindow) and keeps track of where it is, in Dp from the main
// screen's top left, with what the windowing system offers; place_*.go has one for each.
type placer interface {
	view(ev event.Event) bool // takes the native view event Gio sends once the window's made, if it's this system's
	place(saved render.Rect, size render.Point, scale float32)
	frame(scale float32) (render.Rect, bool)
	close()
}
