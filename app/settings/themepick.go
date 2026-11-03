package settings

import (
	"fmt"
	"image"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

func (e *ThemeEditor) startPick() error {
	cur := e.h.CurrentTheme()
	if cur == nil {
		return fmt.Errorf("no theme to pick from yet")
	}
	wasOpen := e.open
	e.open = false
	e.probeTh = theme.Probe(cur)
	e.probe = e.h.Shot(e.probeTh)
	if e.probe == nil {
		e.open = wasOpen
		return fmt.Errorf("this window can't be drawn again to pick from")
	}
	e.picking, e.hover, e.hoverAt = true, "", render.Point{X: -1, Y: -1}
	return nil
}

// probeAt reads what painted p in img: the pixel's token, or the nearest one within a few pixels when p is on an
// antialiased edge. r is the extent of that token's paint around p.
func probeAt(img *image.RGBA, th *theme.Resolved, p image.Point) (string, image.Rectangle) {
	at := func(q image.Point) (string, bool) {
		if !q.In(img.Rect) {
			return "", false
		}
		c := img.RGBAAt(q.X, q.Y)
		return th.Probed(render.Color{R: c.R, G: c.G, B: c.B, A: 255})
	}
	for d := 0; d <= 4; d++ {
		for y := -d; y <= d; y++ {
			for x := -d; x <= d; x++ {
				if max(abs(x), abs(y)) != d {
					continue
				}
				q := p.Add(image.Pt(x, y))
				if name, ok := at(q); ok {
					return name, extent(img, q, 200_000)
				}
			}
		}
	}
	return "", image.Rectangle{}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// extent is the bounding box of the pixels the same colour as p, connected to it, up to limit of them.
func extent(img *image.RGBA, p image.Point, limit int) image.Rectangle {
	want := img.RGBAAt(p.X, p.Y)
	seen := map[image.Point]bool{p: true}
	queue := []image.Point{p}
	r := image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))}
	for len(queue) > 0 && len(seen) < limit {
		q := queue[0]
		queue = queue[1:]
		r = r.Union(image.Rectangle{Min: q, Max: q.Add(image.Pt(1, 1))})
		for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			n := q.Add(d)
			if !seen[n] && n.In(img.Rect) && img.RGBAAt(n.X, n.Y) == want {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return r
}

// buildPick is pick mode over the live window: what's under the pointer, outlined and named; a click picks it.
func (e *ThemeEditor) buildPick(c *ui.Ctx) {
	if !e.picking || c.Theme().IsProbe() {
		return
	}
	th := c.Theme()
	size := c.Size()
	id := c.Next("pick")
	in := c.Interaction(id)
	for _, k := range in.Keys {
		if k.Key == "Esc" {
			e.picking, e.probe = false, nil
			return
		}
	}
	if in.Pointer() != e.hoverAt {
		e.hoverAt = in.Pointer()
		name, r := probeAt(e.probe, e.probeTh, image.Pt(int(in.Pointer().X), int(in.Pointer().Y)))
		e.hover, e.hoverR = name, rectOf(r)
	}
	if in.Clicked && e.hover != "" {
		e.choose(e.hover)
		e.picking, e.probe, e.open = false, nil, true
		return
	}
	pink := th.C(tThemePick)
	c.Box("pick", ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Z: ui.LayerOverlay, Interactive: true, Focusable: true, Trap: true, KeyMode: "themepick",
		Cursor: render.CursorCrosshair, Float: &ui.Float{Point: &render.Point{}}, Help: "shell/theme#pick", Role: ui.RoleDialog,
		Label: "Pick mode: click anything"}, func(c *ui.Ctx) {
		top := render.Point{X: size.X / 2, Y: 52}
		c.Row("banner", ui.Style{Pad: ui.PadXY(12, 5), Gap: 8, Cross: ui.Center, Fill: th.C(theme.BgBase), Stroke: pink, Border: 1, Radius: 12, Z: ui.LayerOverlay + 2,
			Float: &ui.Float{Point: &top, Self: render.Point{X: .5}}}, func(c *ui.Ctx) {
			c.Text("t", "PICK MODE · CLICK ANYTHING · ESC", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .1}, Color: pink})
		})
		pickMark(c, th, e.hover, e.hoverR, render.Rect{Max: size}, ui.LayerOverlay+1)
	})
}

func rectOf(r image.Rectangle) render.Rect {
	return render.Rect{Min: render.Point{X: float32(r.Min.X), Y: float32(r.Min.Y)}, Max: render.Point{X: float32(r.Max.X), Y: float32(r.Max.Y)}}
}

// pickMark outlines r at z and tags it with name over that, above r while that stays inside bounds (window
// coordinates). z 0 keeps the parent's layer.
func pickMark(c *ui.Ctx, th *theme.Resolved, name string, r, bounds render.Rect, z int) {
	if name == "" {
		return
	}
	pink := th.C(tThemePick)
	at := r.Min
	c.Box("outline", ui.Style{W: ui.Px(r.Dx() + 2), H: ui.Px(r.Dy() + 2), Stroke: pink, Border: 1, Radius: 3, Z: z,
		Float: &ui.Float{Point: &render.Point{X: at.X - 1, Y: at.Y - 1}}}, nil)
	tag := render.Point{X: at.X, Y: max(bounds.Min.Y, at.Y-18)}
	c.Row("tag", ui.Style{Pad: ui.PadXY(6, 2), Fill: pink, Radius: 3, Z: z + 1, Float: &ui.Float{Point: &tag}, Role: ui.RoleStatus, Label: name}, func(c *ui.Ctx) {
		c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.SemiBold}, Color: th.C(tThemePickInk)})
	})
}
