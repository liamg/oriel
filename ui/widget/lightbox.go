package widget

import (
	"fmt"
	"image"
	"math"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tLightboxScrim  = theme.Declare("lightbox.scrim", "alpha(#05070a, 92%)")
	tLightboxFg     = theme.Declare("lightbox.fg", "#eceef2")
	tLightboxMuted  = theme.Declare("lightbox.fg.muted", "#a3a9b3")
	tLightboxChrome = theme.Declare("lightbox.chrome", "alpha(#000000, 60%)")
	tImageHover     = theme.Declare("image.hover.border", "accent")
)

// LightboxItem is one image a lightbox can show.
type LightboxItem struct {
	Image   image.Image
	Caption string // what it shows: its alt text
}

type LightboxOpts struct {
	Help string
}

// The lightbox's room around a fitted image: for the close button above, the arrows beside and the caption below.
const (
	lbTop, lbSide, lbBottom = 60, 64, 64
	lbMaxZoom               = 32
	lbDoubleClick           = 400 * time.Millisecond
)

type lightboxState struct {
	shown       bool
	at          int
	zoom        float32      // logical pixels per image pixel; 0 fits the image to the window
	off         render.Point // the image's centre from the room's
	held, moved bool
	from, start render.Point // where a drag started, and the offset then
	clicked     time.Time    // the last click on the zoomed image, for a double-click
}

// Lightbox shows items[*at] large over a scrim while *open, fit to the window. Escape, a click on the scrim, a click on
// the image while it fits, or the ✕ closes it. The wheel zooms around the pointer, a drag pans the zoomed image, a
// double-click or 0 fits it again and 1 shows it at its own size; ←/→ (h/l) move *at between the items.
func Lightbox(c *ui.Ctx, key any, open *bool, items []LightboxItem, at *int, o LightboxOpts) {
	id := c.Next(key)
	st := ui.State[lightboxState](c, id)
	if !*open || len(items) == 0 {
		st.shown = false
		return
	}
	closeIt := func() { *open, st.shown = false, false }
	in := c.Interaction(id)
	for _, k := range in.Keys {
		if k.Mods&^render.ModShift != 0 {
			continue
		}
		switch k.Key {
		case "Esc", "q":
			closeIt()
			return
		case "Left", "h":
			*at--
		case "Right", "l":
			*at++
		}
	}
	*at = max(0, min(*at, len(items)-1))
	if !st.shown || st.at != *at {
		*st = lightboxState{shown: st.shown, at: *at}
	}
	if !st.shown {
		st.shown = true
		c.Focus(id)
	}
	it := items[*at]
	size := c.Size()
	b := it.Image.Bounds()
	iw, ih := float32(max(b.Dx(), 1)), float32(max(b.Dy(), 1))
	room := render.Rect{Min: render.Point{X: lbSide, Y: lbTop}, Max: render.Point{X: max(size.X-lbSide, lbSide+1), Y: max(size.Y-lbBottom, lbTop+1)}}
	centre := render.Point{X: (room.Min.X + room.Max.X) / 2, Y: (room.Min.Y + room.Max.Y) / 2}
	fit := min(1, room.Dx()/iw, room.Dy()/ih)
	zoom := func() float32 {
		if st.zoom == 0 {
			return fit
		}
		return st.zoom
	}
	shown := func() render.Rect {
		z := zoom()
		w, h := iw*z, ih*z
		return render.XYWH(centre.X+st.off.X-w/2, centre.Y+st.off.Y-h/2, w, h)
	}
	pan := func() {
		d := shown()
		st.off.X = panAxis(st.off.X, d.Dx(), centre.X, size.X)
		st.off.Y = panAxis(st.off.Y, d.Dy(), centre.Y, size.Y)
	}
	zoomAt := func(p render.Point, z float32) {
		z = max(fit, min(z, max(fit, lbMaxZoom)))
		d, was := shown(), zoom()
		u := render.Point{X: (p.X - d.Min.X) / was, Y: (p.Y - d.Min.Y) / was}
		if z <= fit {
			st.zoom, st.off = 0, render.Point{}
			return
		}
		st.zoom = z
		st.off = render.Point{X: p.X - u.X*z + iw*z/2 - centre.X, Y: p.Y - u.Y*z + ih*z/2 - centre.Y}
		pan()
	}
	for _, k := range in.Keys {
		switch k.Key {
		case "0":
			st.zoom, st.off = 0, render.Point{}
		case "1":
			zoomAt(centre, 1)
		case "+", "=":
			zoomAt(centre, zoom()*1.25)
		case "-":
			zoomAt(centre, zoom()/1.25)
		}
	}
	if in.Scroll.Y != 0 {
		zoomAt(in.Pointer(), zoom()*float32(math.Exp(float64(-in.Scroll.Y/240))))
	}
	if in.Clicked && !st.moved {
		p := in.Pointer()
		d := shown()
		onImage := p.X >= d.Min.X && p.X < d.Max.X && p.Y >= d.Min.Y && p.Y < d.Max.Y
		switch {
		case !onImage || st.zoom == 0:
			closeIt()
			return
		case c.Now().Sub(st.clicked) < lbDoubleClick:
			st.zoom, st.off, st.clicked = 0, render.Point{}, time.Time{}
		default:
			st.clicked = c.Now()
		}
	}
	if in.Held {
		p := in.Pointer()
		if !st.held {
			st.held, st.moved, st.from, st.start = true, false, p, st.off
		}
		if abs(p.X-st.from.X)+abs(p.Y-st.from.Y) > 3 {
			st.moved = true
		}
		if st.moved && st.zoom != 0 {
			st.off = render.Point{X: st.start.X + p.X - st.from.X, Y: st.start.Y + p.Y - st.from.Y}
			pan()
		}
	} else {
		st.held, st.moved = false, false
	}
	cursor := render.CursorPointer
	switch {
	case st.zoom != 0 && st.held && st.moved:
		cursor = render.CursorGrabbing
	case st.zoom != 0:
		cursor = render.CursorGrab
	}
	caption := it.Caption
	if caption == "" {
		caption = "Image"
	}
	d, img := shown(), it.Image
	fg, muted, chrome := col(c, tLightboxFg), col(c, tLightboxMuted), col(c, tLightboxChrome)
	c.Box(key, ui.Style{W: ui.Px(size.X), H: ui.Px(size.Y), Fill: col(c, tLightboxScrim), Z: ui.LayerDialog, Float: &ui.Float{Point: &render.Point{}},
		Interactive: true, Focusable: true, Trap: true, KeyMode: "lightbox", Wheel: true, Cursor: cursor, NoHover: true, Enter: fade, Exit: fade,
		Role: ui.RoleDialog, Label: caption, Help: o.Help}, func(c *ui.Ctx) {
		c.Paint("img", ui.Px(size.X), ui.Px(size.Y), func(f render.Frame, r render.Rect) { paintImage(f, img, d, r) })
		c.Box("close", ui.Style{Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 1}, Offset: render.Point{X: -16, Y: 14}}}, func(c *ui.Ctx) {
			if lightboxButton(c, "x", "✕", "Close", "Close (Esc)", false, fg, chrome, o.Help) {
				closeIt()
			}
		})
		if len(items) > 1 {
			c.Box("prev", ui.Style{Float: &ui.Float{At: render.Point{Y: .5}, Self: render.Point{Y: .5}, Offset: render.Point{X: 16}}}, func(c *ui.Ctx) {
				if lightboxButton(c, "b", "‹", "Previous image", "Previous (←)", *at == 0, fg, chrome, o.Help) {
					*at--
				}
			})
			c.Box("next", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: .5}, Self: render.Point{X: 1, Y: .5}, Offset: render.Point{X: -16}}}, func(c *ui.Ctx) {
				if lightboxButton(c, "b", "›", "Next image", "Next (→)", *at == len(items)-1, fg, chrome, o.Help) {
					*at++
				}
			})
		}
		c.Row("foot", ui.Style{W: ui.Size{Max: max(size.X-48, 0)}, Pad: ui.PadXY(12, 6), Gap: 14, Cross: ui.Center, Radius: 6, Fill: chrome,
			Float: &ui.Float{At: render.Point{X: .5, Y: 1}, Self: render.Point{X: .5, Y: 1}, Offset: render.Point{Y: -16}}}, func(c *ui.Ctx) {
			c.Text("caption", caption, uiText(c, 12.5, typeset.Medium, fg))
			if len(items) > 1 {
				c.Text("count", fmt.Sprintf("%d / %d", *at+1, len(items)), monoText(11, typeset.Normal, muted, 0))
			}
			c.Text("size", fmt.Sprintf("%d×%d · %d%%", b.Dx(), b.Dy(), int(math.Round(float64(zoom()*100)))), monoText(11, typeset.Normal, muted, 0))
		})
	})
}

// panAxis keeps an image len long along one axis over a window size long: centred while it fits, else covering it.
func panAxis(off, length, centre, size float32) float32 {
	if length <= size {
		return 0
	}
	return max(size-centre-length/2, min(off, length/2-centre))
}

func lightboxButton(c *ui.Ctx, key any, glyph, label, tip string, off bool, fg, fill render.Color, help string) bool {
	if off {
		fg = alpha(fg, .35)
	}
	return Clickable(c, key, ui.Style{W: ui.Px(34), H: ui.Px(34), Radius: 17, Fill: fill, Main: ui.Center, Cross: ui.Center, Disabled: off,
		Label: label, Tooltip: tip, Help: help}, func(c *ui.Ctx) {
		c.Text("g", glyph, uiText(c, 17, typeset.Medium, fg))
	}).Clicked
}

func abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
