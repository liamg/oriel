//go:build !(darwin && !ios)

package gio

import (
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// sideWatcher does nothing: elsewhere Gio reports the side buttons itself, or (X11) not at all.
type sideWatcher struct {
	on func(render.Event)
}

func (*sideWatcher) view(event.Event) {}
func (*sideWatcher) close()           {}
