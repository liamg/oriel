package widget

import (
	"image"
	"image/draw"
	"sync"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// ImageOpts is how an image is shown.
type ImageOpts struct {
	Label string // its accessible name: what it shows
	Help  string
}

// Image draws img w wide, its height kept to its shape, in the device's own pixels: it's scaled once per size and kept.
// It returns the image's id; its rect is where annotations over it go.
func Image(c *ui.Ctx, key any, img image.Image, w float32, o ImageOpts) ui.ID {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || w <= 0 {
		return c.Box(key, ui.Style{W: ui.Px(w)}, nil)
	}
	h := w * float32(b.Dy()) / float32(b.Dx())
	return c.Box(key, ui.Style{W: ui.Px(w), H: ui.Px(h), Role: ui.RoleImage, Label: o.Label, Help: o.Help}, func(c *ui.Ctx) {
		c.Paint("px", ui.Px(w), ui.Px(h), func(f render.Frame, r render.Rect) {
			s := f.Scale()
			f.Image(scaled(img, int(r.Dx()*s+.5), int(r.Dy()*s+.5)), r.Min)
		})
	})
}

type scaleKey struct {
	img  image.Image
	w, h int
}

var scales = struct {
	sync.Mutex
	m map[scaleKey]*image.RGBA
}{m: map[scaleKey]*image.RGBA{}}

// scaled is img at w×h pixels, premultiplied: each pixel the average of the source pixels under it (the nearest one
// when it's being enlarged). The same image at the same size is the same pointer, frame after frame.
func scaled(img image.Image, w, h int) *image.RGBA {
	w, h = max(w, 1), max(h, 1)
	k := scaleKey{img, w, h}
	scales.Lock()
	defer scales.Unlock()
	if out, ok := scales.m[k]; ok {
		return out
	}
	b := img.Bounds()
	src, ok := img.(*image.RGBA)
	if !ok || b.Min != (image.Point{}) {
		src = image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(src, src.Rect, img, b.Min, draw.Src)
	}
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		y0, y1 := y*sh/h, max((y+1)*sh/h, y*sh/h+1)
		for x := range w {
			x0, x1 := x*sw/w, max((x+1)*sw/w, x*sw/w+1)
			var sum [4]int
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[sy*src.Stride:]
				for sx := x0; sx < x1; sx++ {
					p := row[sx*4 : sx*4+4]
					sum[0], sum[1], sum[2], sum[3] = sum[0]+int(p[0]), sum[1]+int(p[1]), sum[2]+int(p[2]), sum[3]+int(p[3])
				}
			}
			n := (y1 - y0) * (x1 - x0)
			d := out.Pix[y*out.Stride+x*4:]
			d[0], d[1], d[2], d[3] = uint8(sum[0]/n), uint8(sum[1]/n), uint8(sum[2]/n), uint8(sum[3]/n)
		}
	}
	if len(scales.m) >= 64 {
		clear(scales.m)
	}
	scales.m[k] = out
	return out
}
