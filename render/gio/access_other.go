//go:build !(darwin && !ios && cgo) && !((linux && !android) || freebsd || openbsd)

package gio

import "gioui.org/io/event"

// noBridge is for platforms with no screen reader bridge: the tree is kept, and nothing reads it.
type noBridge struct{}

func newAccessBridge(*access) accessBridge { return noBridge{} }

func (noBridge) view(event.Event)    {}
func (noBridge) update(accessChange) {}
func (noBridge) activate(bool)       {}
func (noBridge) close()              {}
