package widget

import (
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// The window's areas, which a frame lays out with Area and what floats over them (pages, the HUD, banners, windows)
// asks for with AreaRect instead of assuming the frame's sizes.
const (
	AreaPage   = "page"   // what a page covers
	AreaTop    = "top"    // across the top: the title bar
	AreaSide   = "side"   // beside the main area: the rail; a page leaves it showing
	AreaMain   = "main"   // the main area
	AreaBottom = "bottom" // along the bottom: the status line
)

type areasKey struct{}

type areaMark struct {
	id ui.ID
	at int // the frame that laid it out last
}

type areasState struct{ marks map[string]areaMark }

// Area is a box laid out as st, named name: one of the window's areas.
func Area(c *ui.Ctx, name string, key any, st ui.Style, body func(c *ui.Ctx)) ui.ID {
	areas := ui.State[areasState](c, ui.Child(0, areasKey{}))
	if areas.marks == nil {
		areas.marks = map[string]areaMark{}
	}
	areas.marks[name] = areaMark{id: c.Next(key), at: c.Frame()}
	return c.Box(key, st, body)
}

// mark is the area named name, when the frame laid it out this frame or last.
func mark(c *ui.Ctx, name string) (areaMark, bool) {
	m, ok := ui.State[areasState](c, ui.Child(0, areasKey{})).marks[name]
	return m, ok && m.at >= c.Frame()-1
}

// AreaRect is where the area named name was laid out last frame; false when the frame has none, or hasn't laid it
// out yet.
func AreaRect(c *ui.Ctx, name string) (render.Rect, bool) {
	m, ok := mark(c, name)
	if !ok {
		return render.Rect{}, false
	}
	r, ok := c.Rect(m.id)
	return r, ok && r != render.Rect{}
}

// areaPending says the frame has the area named name but hasn't laid it out yet: its first frame.
func areaPending(c *ui.Ctx, name string) bool {
	_, has := mark(c, name)
	_, laid := AreaRect(c, name)
	return has && !laid
}

// AreaFloat floats a box against the area named name, as ui.Float's At, Self and Offset place it, laid out with the
// area in the same frame. An area the frame doesn't have is empty, where it would be: no top area is the window's top
// edge.
func AreaFloat(c *ui.Ctx, name string, at, self, offset render.Point) *ui.Float {
	if m, ok := mark(c, name); ok {
		return &ui.Float{To: m.id, At: at, Self: self, Offset: offset}
	}
	r := missingArea(c, name)
	p := render.Point{X: r.Min.X + at.X*r.Dx(), Y: r.Min.Y + at.Y*r.Dy()}
	return &ui.Float{Point: &p, Self: self, Offset: offset}
}

// AreaOr is AreaRect, or where the area would be, empty, when the frame has none: the whole window for the page and
// main areas.
func AreaOr(c *ui.Ctx, name string) render.Rect {
	if r, ok := AreaRect(c, name); ok {
		return r
	}
	return missingArea(c, name)
}

func missingArea(c *ui.Ctx, name string) render.Rect {
	w := render.Rect{Max: c.Size()}
	switch name {
	case AreaTop:
		w.Max.Y = 0
	case AreaBottom:
		w.Min.Y = w.Max.Y
	case AreaSide:
		w.Max.X = 0
	}
	return w
}
