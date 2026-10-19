package widget

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

type SliderOpts struct {
	Min, Max, Step float64
	Width          ui.Size
	Label          string                       // for assistive technology, with the value
	Track          func(t float64) render.Color // the track's colour at t (0..1); a plain track without
	Help           string
}

// Slider is a 12px track with a round thumb (23-theme's L, C and H). Click or drag along it; h/l and the arrows
// step, shift for ten. It returns true on a frame that changed v.
func Slider(c *ui.Ctx, key any, v *float64, o SliderOpts) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	old := *v
	passOn(c, id, in.Keys, func(k render.KeyEvent) bool { return hasKey([]render.KeyEvent{k}, "h", "l", "Left", "Right") })
	span := o.Max - o.Min
	if r, ok := c.Rect(id); ok && in.Held && r.Dx() > 0 {
		t := float64((in.Pointer().X - r.Min.X) / r.Dx())
		*v = o.Min + math.Max(0, math.Min(1, t))*span
	}
	for _, k := range in.Keys {
		step := o.Step
		if step == 0 {
			step = span / 100
		}
		if k.Mods == render.ModShift {
			step *= 10
		}
		switch k.Key {
		case "h", "Left":
			*v -= step
		case "l", "Right":
			*v += step
		}
	}
	if o.Step > 0 && *v != old {
		*v = snap(*v, o.Step)
	}
	*v = math.Max(o.Min, math.Min(o.Max, *v))
	t := float32(0)
	if span > 0 {
		t = float32((*v - o.Min) / span)
	}
	bg, fg, border := col(c, tInputBg), col(c, tInputFg), col(c, tInputBorder)
	if in.FocusVisible {
		border = col(c, tInputFocus)
	}
	c.Box(key, ui.Style{W: o.Width, H: ui.Px(12), Interactive: true, Focusable: true, KeyMode: "slider", Cursor: render.CursorPointer,
		Help: o.Help, Role: ui.RoleSlider, Label: fmt.Sprintf("%s %.3g", o.Label, *v)}, func(c *ui.Ctx) {
		c.Paint("track", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) { paintSlider(f, r, t, o.Track, bg, fg, border) })
	})
	return *v != old
}

// snap rounds v to a multiple of step, to step's decimal places.
func snap(v, step float64) float64 {
	places := 0
	if s := strconv.FormatFloat(step, 'f', -1, 64); strings.Contains(s, ".") {
		places = len(s) - strings.Index(s, ".") - 1
	}
	f, _ := strconv.ParseFloat(strconv.FormatFloat(math.Round(v/step)*step, 'f', places, 64), 64)
	return f
}

func paintSlider(f render.Frame, r render.Rect, t float32, track func(float64) render.Color, bg, fg, border render.Color) {
	bar := render.Rect{Min: render.Point{X: r.Min.X, Y: r.Min.Y + 4}, Max: render.Point{X: r.Max.X, Y: r.Max.Y - 4}}
	if track == nil {
		f.Rect(bar, render.Style{Fill: bg, Radius: 2, Stroke: border, Width: 1})
	} else {
		const n = 48
		w := bar.Dx() / n
		f.PushClip(bar, 2)
		for i := range n {
			f.Rect(render.XYWH(bar.Min.X+float32(i)*w, bar.Min.Y, w+.5, bar.Dy()), render.Style{Fill: track((float64(i) + .5) / n)})
		}
		f.Pop()
	}
	x := r.Min.X + t*r.Dx()
	f.Path(render.Ellipse(render.XYWH(x-6, r.Min.Y, 12, 12)), render.Style{Fill: fg, Stroke: border, Width: 1.5})
}
