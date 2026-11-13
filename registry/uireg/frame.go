package uireg

import (
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// Window is what the window's frame is chosen by.
type Window struct {
	Session *core.SessionState // the session in front, while it's live (not archived); nil for none
	Dir     string             // the directory the window is open on; "" for none
}

// Area is where a frame puts a region.
type Area int

const (
	AreaTop    Area = iota // across the top: the title bar
	AreaLeft               // down the left, beside the main region: the rail
	AreaMain               // what's left; pages cover it and the top
	AreaBottom             // along the bottom: the status line
	AreaOver               // floats over everything: notifications
)

// Region is a view a frame puts in an area, built with the window's ViewCtx.
type Region struct {
	View string
	Area Area
}

// Frame is a shape of the window: its regions, while When holds. The window is the first frame by Order whose When
// holds (nil always does); views named app.overlay.* float over any of them. A Global frame has no session: its
// regions are built with ViewCtx.Global and no ViewCtx.Session.
type Frame struct {
	ID      string
	Order   int
	When    func(Window) bool
	Regions []Region
	Global  bool
	Source  string
}

// Global is a part of a region that doesn't need a session (a status line segment: the error counts), so a Global
// frame draws it too. Parts that aren't are left out there.
type Global interface{ Global() }

// GlobalView is View marked Global.
type GlobalView struct{ View }

func (GlobalView) Global()        {}
func (g GlobalView) Unwrap() View { return g.View }

// IsGlobal says whether v is marked Global.
func IsGlobal(v View) bool {
	_, ok := As[Global](v)
	return ok
}

func (Frame) ContributionKind() string   { return "frame" }
func (f Frame) ContributionName() string { return f.ID }

// FrameFor is the frame the window takes for w.
func FrameFor(r registry.Contributions, w Window) (Frame, bool) {
	var fs []Frame
	fs = append(fs, registry.ContributionsOf[Frame](r)...)
	slices.SortStableFunc(fs, func(a, b Frame) int { return a.Order - b.Order })
	for _, f := range fs {
		ok := f.When == nil
		if !ok {
			registry.Call(func() error { ok = f.When(w); return nil })
		}
		if ok {
			return f, true
		}
	}
	return Frame{}, false
}
