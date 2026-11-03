package widget

import (
	"fmt"
	"image"
	"image/draw"
	"math"
	"sync"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// ImageOpts is how an image is shown.
type ImageOpts struct {
	Label string // its accessible name: what it shows, and its caption in the lightbox
	Help  string
	Zoom  bool   // a click opens it in the lightbox, large over the window
	Group string // the images the lightbox's ← and → move between, as one document's; alone if ""
}

// Image draws img w wide, its height kept to its shape, in the device's own pixels: it's scaled once per size and kept.
// It returns the image's id; its rect is where annotations over it go.
func Image(c *ui.Ctx, key any, img image.Image, w float32, o ImageOpts) ui.ID {
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || w <= 0 {
		return c.Box(key, ui.Style{W: ui.Px(w)}, nil)
	}
	h := w * float32(b.Dy()) / float32(b.Dx())
	ic := imageCacheOf(c)
	return picture(c, key, w, h, func(f render.Frame, r render.Rect) { ic.paint(f, img, r, r) }, LightboxItem{Image: img, Caption: o.Label}, o)
}

// Vector is a picture that draws itself at any size, as a diagram does: crisp however far the lightbox zooms it.
type Vector struct {
	Size render.Point                        // its own size, in logical pixels
	Draw func(f render.Frame, r render.Rect) // paints it scaled into r
	Fill render.Color                        // laid under it in the lightbox, as an image's own pixels would be
	Tips []VectorTip                         // tooltips over parts of it, such as text it draws cut short
}

// VectorTip is a tooltip over part of a Vector: Rect is in the vector's own logical pixels.
type VectorTip struct {
	Rect render.Rect
	Text string
}

// tipAt is the tooltip of v's part under p, v drawn into r; "" over none.
func (v *Vector) tipAt(r render.Rect, p render.Point) string {
	if v == nil || v.Size.X <= 0 {
		return ""
	}
	k := r.Dx() / v.Size.X
	for _, t := range v.Tips {
		if p.X >= r.Min.X+t.Rect.Min.X*k && p.X < r.Min.X+t.Rect.Max.X*k && p.Y >= r.Min.Y+t.Rect.Min.Y*k && p.Y < r.Min.Y+t.Rect.Max.Y*k {
			return t.Text
		}
	}
	return ""
}

// VectorImage draws v w wide, its height kept to its shape, and is otherwise an Image: zoomable into the lightbox,
// in a group with the document's images.
func VectorImage(c *ui.Ctx, key any, v Vector, w float32, o ImageOpts) ui.ID {
	if v.Size.X <= 0 || v.Size.Y <= 0 || w <= 0 || v.Draw == nil {
		return c.Box(key, ui.Style{W: ui.Px(w)}, nil)
	}
	h := w * v.Size.Y / v.Size.X
	return picture(c, key, w, h, v.Draw, LightboxItem{Vector: &v, Caption: o.Label}, o)
}

// picture is an image or a vector w×h, drawn by paint, with its edge while hovered and a click into the lightbox. A
// vector's tooltip is that of its part under the pointer.
func picture(c *ui.Ctx, key any, w, h float32, paint func(f render.Frame, r render.Rect), item LightboxItem, o ImageOpts) ui.ID {
	pixels := func(c *ui.Ctx) { c.Paint("px", ui.Px(w), ui.Px(h), paint) }
	id := c.Next(key)
	in := c.Interaction(id)
	tip := ""
	if r, ok := c.Rect(id); ok && in.Hovered {
		tip = item.Vector.tipAt(r, in.Pointer())
	}
	if !o.Zoom {
		tips := item.Vector != nil && len(item.Vector.Tips) > 0
		return c.Box(key, ui.Style{W: ui.Px(w), H: ui.Px(h), Role: ui.RoleImage, Label: o.Label, Help: o.Help, Tooltip: tip, Interactive: tips}, pixels)
	}
	lightbox := groupImageBuilt(c, id, item, o)
	Clickable(c, key, ui.Style{W: ui.Px(w), H: ui.Px(h), NoHover: true, Role: ui.RoleImage, Label: o.Label, Help: o.Help, Tooltip: tip}, func(c *ui.Ctx) {
		pixels(c)
		if in.Hovered {
			edge := col(c, tImageHover)
			c.Box("hover", ui.Style{Float: &ui.Float{}, Z: ui.LayerContent}, func(c *ui.Ctx) {
				c.Paint("edge", ui.Px(w), ui.Px(h), func(f render.Frame, r render.Rect) {
					f.Rect(r, render.Style{Stroke: edge, Width: 1.5})
				})
			})
		}
		if lightbox != nil {
			lightbox(c)
		}
	})
	return id
}

// imageGroups are the zoomable images built in the window, by group, and which one the lightbox shows.
type imageGroups struct {
	frame  int
	groups map[string]*imageGroup
	open   string // the group the lightbox shows; "" while it's closed
	at     ui.ID  // the image it shows
	seen   int    // the frame it was last drawn or opened in
}

// imageGroup is a group's images built this frame, and those built the frame before.
type imageGroup struct {
	frame       int
	built, last []groupImage
}

type groupImage struct {
	id   ui.ID
	item LightboxItem
}

type imagesKey struct{}

// groupImageBuilt adds a zoomable image or vector to its group, opens the lightbox on it when it's clicked, and
// returns what draws the lightbox when this is the group's first one this frame and the lightbox shows the group.
func groupImageBuilt(c *ui.Ctx, id ui.ID, item LightboxItem, o ImageOpts) func(c *ui.Ctx) {
	gs := ui.State[imageGroups](c, ui.Child(0, imagesKey{}))
	frame := c.Frame()
	if gs.groups == nil {
		gs.groups = map[string]*imageGroup{}
	}
	if gs.frame != frame {
		gs.frame = frame
		for k, g := range gs.groups {
			if g.frame < frame-1 {
				delete(gs.groups, k)
			}
		}
		if gs.open != "" && gs.seen < frame-1 {
			gs.open = ""
		}
	}
	name := o.Group
	if name == "" {
		name = fmt.Sprint(uint64(id))
	}
	g := gs.groups[name]
	if g == nil {
		g = &imageGroup{}
		gs.groups[name] = g
	}
	if g.frame != frame {
		g.frame, g.last, g.built = frame, g.built, nil
	}
	g.built = append(g.built, groupImage{id, item})
	if c.Interaction(id).Clicked {
		gs.open, gs.at, gs.seen = name, id, frame
	}
	if gs.open != name || len(g.built) != 1 {
		return nil
	}
	return func(c *ui.Ctx) {
		list := g.last
		at := groupIndex(list, gs.at)
		if at < 0 {
			list = g.built
			at = groupIndex(list, gs.at)
		}
		if at < 0 {
			gs.open = ""
			return
		}
		items := make([]LightboxItem, len(list))
		for i, gi := range list {
			items[i] = gi.item
		}
		open := true
		Lightbox(c, imagesKey{}, &open, items, &at, LightboxOpts{Help: o.Help})
		gs.seen = frame
		if !open {
			gs.open = ""
			return
		}
		gs.at = list[at].id
	}
}

func groupIndex(list []groupImage, id ui.ID) int {
	for i, gi := range list {
		if gi.id == id {
			return i
		}
	}
	return -1
}

// paint draws img into d, a rect in the frame's logical pixels, as far as it shows in view: only the part in view
// is scaled, so a large image zoomed far in costs no more than the view.
func (ic *imageCache) paint(f render.Frame, img image.Image, d, view render.Rect) {
	vis := render.Rect{
		Min: render.Point{X: max(d.Min.X, view.Min.X), Y: max(d.Min.Y, view.Min.Y)},
		Max: render.Point{X: min(d.Max.X, view.Max.X), Y: min(d.Max.Y, view.Max.Y)},
	}
	if vis.Dx() <= 0 || vis.Dy() <= 0 {
		return
	}
	s := f.Scale()
	w, h := int(d.Dx()*s+.5), int(d.Dy()*s+.5)
	if vis == d {
		f.Image(ic.scaled(img, w, h), d.Min)
		return
	}
	part := image.Rect(int(math.Floor(float64((vis.Min.X-d.Min.X)*s))), int(math.Floor(float64((vis.Min.Y-d.Min.Y)*s))),
		int(math.Ceil(float64((vis.Max.X-d.Min.X)*s))), int(math.Ceil(float64((vis.Max.Y-d.Min.Y)*s))))
	if out := ic.scaledPart(img, w, h, part); out != nil {
		f.Image(out, render.Point{X: d.Min.X + float32(part.Min.X)/s, Y: d.Min.Y + float32(part.Min.Y)/s})
	}
}

type scaleKey struct {
	img  image.Image
	w, h int
	part image.Rectangle
}

// imageCache is a UI's images as drawn: scales keeps whole images at the sizes they're drawn at, parts the few
// parts of images drawn zoomed in, and sources images converted to premultiplied RGBA at their own size.
type imageCache struct {
	scales, parts *scaleCache
	sources       sourceCache
}

func imageCacheOf(c *ui.Ctx) *imageCache {
	return ui.Keep(c, func() *imageCache {
		return &imageCache{scales: newScaleCache(64), parts: newScaleCache(4), sources: sourceCache{m: map[image.Image]*image.RGBA{}}}
	})
}

type scaleCache struct {
	sync.Mutex
	m    map[scaleKey]*image.RGBA
	size int
}

func newScaleCache(n int) *scaleCache { return &scaleCache{m: map[scaleKey]*image.RGBA{}, size: n} }

// maxPart is the most pixels a scaled image or part may have.
const maxPart = 8192 * 8192

// scaled is img at w×h pixels, premultiplied: shrunk with a Catmull-Rom filter, or enlarged to the nearest source
// pixel. The same image at the same size is the same pointer, frame after frame.
func (ic *imageCache) scaled(img image.Image, w, h int) *image.RGBA {
	w, h = max(w, 1), max(h, 1)
	return ic.scaledPart(img, w, h, image.Rect(0, 0, w, h))
}

// scaledPart is the part of img scaled to w×h that's in part, as scaled draws it; nil when part is empty or too big.
func (ic *imageCache) scaledPart(img image.Image, w, h int, part image.Rectangle) *image.RGBA {
	w, h = max(w, 1), max(h, 1)
	part = part.Intersect(image.Rect(0, 0, w, h))
	if part.Empty() || part.Dx()*part.Dy() > maxPart {
		return nil
	}
	cache := ic.scales
	if part != image.Rect(0, 0, w, h) {
		cache = ic.parts
	}
	k := scaleKey{img, w, h, part}
	cache.Lock()
	defer cache.Unlock()
	if out, ok := cache.m[k]; ok {
		return out
	}
	src := ic.sources.rgbaOf(img)
	var out *image.RGBA
	if w < src.Rect.Dx() || h < src.Rect.Dy() {
		out = shrink(src, w, h, part)
	} else {
		out = enlarge(src, w, h, part)
	}
	if len(cache.m) >= cache.size {
		clear(cache.m)
	}
	cache.m[k] = out
	return out
}

// enlarge is the part of src scaled up to w×h, each pixel the nearest source pixel's.
func enlarge(src *image.RGBA, w, h int, part image.Rectangle) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	out := image.NewRGBA(image.Rect(0, 0, part.Dx(), part.Dy()))
	for y := part.Min.Y; y < part.Max.Y; y++ {
		row := src.Pix[(y*sh/h)*src.Stride:]
		d := out.Pix[(y-part.Min.Y)*out.Stride:]
		for x := part.Min.X; x < part.Max.X; x++ {
			copy(d[(x-part.Min.X)*4:(x-part.Min.X)*4+4], row[(x*sw/w)*4:])
		}
	}
	return out
}

// shrink is the part of src scaled down to w×h through a Catmull-Rom filter widened to the scale, one axis at a time,
// which keeps small text sharper than averaging.
func shrink(src *image.RGBA, w, h int, part image.Rectangle) *image.RGBA {
	xs, ys := filterTaps(src.Rect.Dx(), w, part.Min.X, part.Max.X), filterTaps(src.Rect.Dy(), h, part.Min.Y, part.Max.Y)
	top, bottom := math.MaxInt, 0
	for _, t := range ys {
		top, bottom = min(top, t.from), max(bottom, t.from+len(t.w))
	}
	// rows are the source rows the part reads, each already filtered across.
	pw := part.Dx()
	rows := make([]float32, (bottom-top)*pw*4)
	for sy := top; sy < bottom; sy++ {
		in, o := src.Pix[sy*src.Stride:], rows[(sy-top)*pw*4:]
		for x, t := range xs {
			var sum [4]float32
			for i, k := range t.w {
				p := in[(t.from+i)*4:]
				sum[0], sum[1], sum[2], sum[3] = sum[0]+k*float32(p[0]), sum[1]+k*float32(p[1]), sum[2]+k*float32(p[2]), sum[3]+k*float32(p[3])
			}
			copy(o[x*4:x*4+4], sum[:])
		}
	}
	out := image.NewRGBA(image.Rect(0, 0, pw, part.Dy()))
	for y, t := range ys {
		d := out.Pix[y*out.Stride:]
		for x := range pw {
			var sum [4]float32
			for i, k := range t.w {
				r := rows[((t.from+i-top)*pw+x)*4:]
				sum[0], sum[1], sum[2], sum[3] = sum[0]+k*r[0], sum[1]+k*r[1], sum[2]+k*r[2], sum[3]+k*r[3]
			}
			a := clampByte(sum[3])
			d[x*4], d[x*4+1], d[x*4+2], d[x*4+3] = min(clampByte(sum[0]), a), min(clampByte(sum[1]), a), min(clampByte(sum[2]), a), a
		}
	}
	return out
}

// taps are the source pixels one output pixel reads along an axis, from from, and their weights.
type taps struct {
	from int
	w    []float32
}

// filterTaps are the taps of output pixels lo to hi of n along an axis src pixels long, through a Catmull-Rom filter
// widened by the scale. Weights past an edge go to the edge pixel.
func filterTaps(src, n, lo, hi int) []taps {
	scale := max(float64(src)/float64(n), 1)
	support := 2 * scale
	out := make([]taps, hi-lo)
	for o := lo; o < hi; o++ {
		centre := (float64(o)+.5)*scale - .5
		first, last := int(math.Ceil(centre-support)), int(math.Floor(centre+support))
		from, to := max(first, 0), min(last, src-1)
		ws := make([]float64, to-from+1)
		var sum float64
		for i := first; i <= last; i++ {
			k := catmullRom((float64(i) - centre) / scale)
			ws[min(max(i, 0), src-1)-from] += k
			sum += k
		}
		t := taps{from: from, w: make([]float32, len(ws))}
		for i, k := range ws {
			t.w[i] = float32(k / sum)
		}
		out[o-lo] = t
	}
	return out
}

func catmullRom(x float64) float64 {
	x = math.Abs(x)
	switch {
	case x < 1:
		return (1.5*x-2.5)*x*x + 1
	case x < 2:
		return ((-.5*x+2.5)*x-4)*x + 2
	}
	return 0
}

func clampByte(v float32) uint8 { return uint8(min(max(v+.5, 0), 255)) }

// sourceCache converts each image to premultiplied RGBA once.
type sourceCache struct {
	sync.Mutex
	m map[image.Image]*image.RGBA
}

func (sc *sourceCache) rgbaOf(img image.Image) *image.RGBA {
	b := img.Bounds()
	if r, ok := img.(*image.RGBA); ok && b.Min == (image.Point{}) {
		return r
	}
	sc.Lock()
	defer sc.Unlock()
	if r, ok := sc.m[img]; ok {
		return r
	}
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Rect, img, b.Min, draw.Src)
	if len(sc.m) >= 16 {
		clear(sc.m)
	}
	sc.m[img] = r
	return r
}
