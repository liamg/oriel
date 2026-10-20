//go:build !(darwin && !ios)

package gio

import "gioui.org/io/event"

// hoverWatcher does nothing: X11 and Wayland send moves only to the window under the pointer, with a leave.
type hoverWatcher struct{}

func (*hoverWatcher) view(event.Event) {}
func (*hoverWatcher) close()           {}
