//go:build !(darwin && !ios)

package gio

import "gioui.org/io/event"

// occlusionWatcher does nothing: X11 and Wayland don't say when a window is covered, only (through Gio) minimised.
type occlusionWatcher struct {
	on func(occluded bool)
}

func (*occlusionWatcher) view(event.Event) {}
func (*occlusionWatcher) close()           {}
