package widget

import (
	"fmt"
	"image"
	"math"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tCompareBg      = theme.Declare("compare.bg", "bg.base")
	tCompareDivider = theme.Declare("compare.divider", "accent")
	tCompareHandle  = theme.Declare("compare.handle.fg", "accent.ink")
)

type CompareOpts struct {
	Before, After string // the two sides' names, shown over their corners: "Old", "New"
	Label         string // its accessible name
	Help          string
}

type compareState struct {
	set      bool
	at       float32 // where the divider is, 0 (all after) to 1 (all before)
	lightbox bool
	shown    int
}

// Compare draws before over after, w wide, with a divider dragged across them: before shows left of it and after
// right of it. A drag or a click anywhere moves the divider; ←/→ (h/l) move it by a twentieth when it's focused. ⤢
// shows the two in the lightbox. Both are drawn at one scale, the wider w wide, from the top left.
func Compare(c *ui.Ctx, key any, before, after image.Image, w float32, o CompareOpts) {
	id := c.Next(key)
	st := ui.State[compareState](c, id)
	if !st.set {
		st.at, st.set = .5, true
	}
	in := c.Interaction(id)
	if r, ok := c.Rect(id); ok && in.Held && r.Dx() > 0 {
		st.at = (in.Pointer().X - r.Min.X) / r.Dx()
	}
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "Left", "h"):
			st.at -= .05
		case hasKey([]render.KeyEvent{k}, "Right", "l"):
			st.at += .05
		}
	}
	passOn(c, id, in.Keys, func(k render.KeyEvent) bool { return hasKey([]render.KeyEvent{k}, "Left", "Right", "h", "l") })
	st.at = max(0, min(st.at, 1))
	bb, ab := before.Bounds(), after.Bounds()
	k := w / float32(max(bb.Dx(), ab.Dx(), 1))
	bw, bh := float32(bb.Dx())*k, float32(bb.Dy())*k
	aw, ah := float32(ab.Dx())*k, float32(ab.Dy())*k
	h := max(bh, ah)
	at := st.at
	bg, line, ink := col(c, tCompareBg), col(c, tCompareDivider), col(c, tCompareHandle)
	c.Box(key, ui.Style{W: ui.Px(w), H: ui.Px(h), Clip: true, Interactive: true, Focusable: true, KeyMode: "compare", Cursor: render.CursorResizeCol,
		NoHover: true, Role: ui.RoleSlider, Label: o.Label, Value: fmt.Sprintf("%d%%", int(math.Round(float64(at*100)))), Help: o.Help}, func(c *ui.Ctx) {
		c.Paint("px", ui.Px(w), ui.Px(h), func(f render.Frame, r render.Rect) {
			split := r.Min.X + at*r.Dx()
			f.Rect(r, render.Style{Fill: bg})
			paintImage(f, after, render.XYWH(r.Min.X, r.Min.Y, aw, ah), r)
			left := render.Rect{Min: r.Min, Max: render.Point{X: split, Y: r.Max.Y}}
			f.PushClip(left, 0)
			f.Rect(left, render.Style{Fill: bg})
			paintImage(f, before, render.XYWH(r.Min.X, r.Min.Y, bw, bh), r)
			f.Pop()
			f.Rect(render.XYWH(split-1, r.Min.Y, 2, r.Dy()), render.Style{Fill: line})
			mid := r.Min.Y + r.Dy()/2
			f.Path(render.Ellipse(render.XYWH(split-11, mid-11, 22, 22)), render.Style{Fill: line})
			for _, d := range []float32{-1, 1} {
				var p render.Path
				p.MoveTo(render.Point{X: split + d*3, Y: mid - 4}).LineTo(render.Point{X: split + d*7, Y: mid}).LineTo(render.Point{X: split + d*3, Y: mid + 4})
				f.Path(&p, render.Style{Stroke: ink, Width: 1.5})
			}
		})
		corner := func(k string, text string, x float32) {
			if text == "" {
				return
			}
			c.Row(k, ui.Style{Pad: ui.PadXY(6, 2), Radius: 3, Fill: col(c, tLightboxChrome),
				Float: &ui.Float{At: render.Point{X: x}, Self: render.Point{X: x}, Offset: render.Point{X: 8 - 16*x, Y: 8}}}, func(c *ui.Ctx) {
				c.Text("t", text, monoText(10, typeset.Medium, col(c, tLightboxFg), .04))
			})
		}
		corner("before", o.Before, 0)
		corner("after", o.After, 1)
		c.Box("zoom", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: 1}, Self: render.Point{X: 1, Y: 1}, Offset: render.Point{X: -8, Y: -8}}}, func(c *ui.Ctx) {
			fg := col(c, tLightboxFg)
			if Clickable(c, "b", ui.Style{W: ui.Px(28), H: ui.Px(28), Radius: 14, Fill: col(c, tLightboxChrome), Label: "Show large",
				Tooltip: "Show the two large", Help: o.Help}, func(c *ui.Ctx) {
				c.Paint("icon", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) { paintExpand(f, r, fg) })
			}).Clicked {
				st.lightbox = true
			}
		})
		items := []LightboxItem{{Image: before, Caption: o.Before}, {Image: after, Caption: o.After}}
		Lightbox(c, "lightbox", &st.lightbox, items, &st.shown, LightboxOpts{Help: o.Help})
	})
}

// paintExpand draws two arrows out to opposite corners in the middle of r: "show it large".
func paintExpand(f render.Frame, r render.Rect, fg render.Color) {
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	for _, d := range []float32{-1, 1} {
		var p render.Path
		tip := render.Point{X: cx + d*6, Y: cy - d*6}
		p.MoveTo(render.Point{X: tip.X - d*4, Y: tip.Y}).LineTo(tip).LineTo(render.Point{X: tip.X, Y: tip.Y + d*4})
		p.MoveTo(tip).LineTo(render.Point{X: cx + d*1.5, Y: cy - d*1.5})
		f.Path(&p, render.Style{Stroke: fg, Width: 1.5})
	}
}
