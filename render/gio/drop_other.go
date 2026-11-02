//go:build !(darwin && !ios)

package gio

import "gioui.org/io/event"

// dropWatcher does nothing: Gio takes no drags from other apps on X11 or Wayland.
type dropWatcher struct {
	on func(paths []string, x, y float64)
}

func (*dropWatcher) view(event.Event) {}
func (*dropWatcher) close()           {}
