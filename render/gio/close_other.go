//go:build !(darwin && !ios)

package gio

import "gioui.org/io/event"

// closeWatcher does nothing: elsewhere Gio's DestroyEvent comes as the window closes.
type closeWatcher struct {
	on func()
}

func (*closeWatcher) view(event.Event) {}
func (*closeWatcher) close()           {}
