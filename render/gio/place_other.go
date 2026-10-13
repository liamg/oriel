//go:build !(darwin && !ios) && !(((linux && !android) || freebsd || openbsd) && !nox11)

package gio

import (
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// noPlacer is for windowing systems a window can't place itself on, such as Wayland's: it keeps its size only.
type noPlacer struct{}

func newPlacer() placer { return noPlacer{} }

func (noPlacer) view(event.Event) bool                    { return false }
func (noPlacer) place(render.Rect, render.Point, float32) {}
func (noPlacer) frame(float32) (render.Rect, bool)        { return render.Rect{}, false }
func (noPlacer) close()                                   {}
