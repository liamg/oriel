package app

import (
	"fmt"
	"image"
	"math"
	"os"
	"sync/atomic"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// icons are the built-in icon set, drawn as strokes in a 16-unit square, so they don't depend on a font having
// the glyph. A rail or start entry names one in Icon; any other Icon is drawn as text.
var icons = map[string]func(p *iconPen){
	"files": func(p *iconPen) {
		p.poly(true, 2, 3.5, 6.5, 3.5, 8, 5, 14, 5, 14, 13, 2, 13)
	},
	"search": func(p *iconPen) {
		p.circle(7, 7, 4.5)
		p.line(10.4, 10.4, 14, 14)
	},
	"git": func(p *iconPen) {
		p.circle(4.5, 3.5, 1.8)
		p.circle(4.5, 12.5, 1.8)
		p.circle(11.5, 5.5, 1.8)
		p.line(4.5, 5.3, 4.5, 10.7)
		p.curve(11.5, 7.3, 11.5, 10, 4.5, 9, 4.5, 10.7)
	},
	"requests": func(p *iconPen) {
		p.poly(true, 8, 1.5, 14.5, 8, 8, 14.5, 1.5, 8)
		p.poly(true, 8, 5, 11, 8, 8, 11, 5, 8)
	},
	"debug": func(p *iconPen) {
		p.poly(true, 4, 2.5, 13.5, 8, 4, 13.5)
	},
	"tests": func(p *iconPen) {
		p.poly(false, 2.5, 8.5, 6.5, 12.5, 13.5, 3.5)
	},
	"settings": func(p *iconPen) {
		p.circle(8, 8, 2.2)
		p.circle(8, 8, 4.6)
		for i := range 8 {
			a := float64(i) * math.Pi / 4
			p.line(8+4.6*cos(a), 8+4.6*sin(a), 8+6.6*cos(a), 8+6.6*sin(a))
		}
	},
	"comment": func(p *iconPen) {
		p.poly(true, 2, 2.5, 14, 2.5, 14, 11, 8, 11, 4.5, 14, 4.5, 11, 2, 11)
	},
	"pullrequest": func(p *iconPen) {
		p.circle(4, 3.5, 1.8)
		p.circle(4, 12.5, 1.8)
		p.circle(12, 12.5, 1.8)
		p.line(4, 5.3, 4, 10.7)
		p.poly(false, 12, 10.7, 12, 5.5, 8, 5.5)
		p.poly(false, 9.8, 3.7, 8, 5.5, 9.8, 7.3)
	},
	"ship": func(p *iconPen) {
		p.line(2, 5.5, 12, 5.5)
		p.line(2, 10.5, 12, 10.5)
		p.poly(false, 10, 3, 13.5, 5.5, 10, 8)
		p.poly(false, 10, 8, 13.5, 10.5, 10, 13)
	},
	"plus": func(p *iconPen) {
		p.line(8, 3, 8, 13)
		p.line(3, 8, 13, 8)
	},
	"resume": func(p *iconPen) {
		p.curve(12.5, 8, 12.5, 11, 10, 13, 8, 13)
		p.curve(8, 13, 5, 13, 3.5, 10.5, 3.5, 8)
		p.curve(3.5, 8, 3.5, 5, 6, 3, 8, 3)
		p.curve(8, 3, 10, 3, 11.5, 4, 12.3, 5.5)
		p.poly(false, 12.6, 2.5, 12.6, 5.8, 9.4, 5.8)
	},
	"agent": func(p *iconPen) {
		p.poly(false, 5, 3, 10.5, 8, 5, 13)
	},
	"stats": func(p *iconPen) {
		p.line(3.5, 13.5, 3.5, 9)
		p.line(8, 13.5, 8, 3)
		p.line(12.5, 13.5, 12.5, 6.5)
	},
	"usage": func(p *iconPen) {
		p.curve(2.5, 12, 2.5, 6.5, 5.5, 4, 8, 4)
		p.curve(8, 4, 10.5, 4, 13.5, 6.5, 13.5, 12)
		p.line(8, 11.5, 11, 7)
	},
	"workspace": func(p *iconPen) {
		p.poly(true, 8, 1.5, 14, 4.5, 14, 11.5, 8, 14.5, 2, 11.5, 2, 4.5)
		p.poly(false, 2, 4.5, 8, 7.5, 14, 4.5)
		p.line(8, 7.5, 8, 14.5)
	},
	"repos": func(p *iconPen) {
		p.line(3, 4, 13, 4)
		p.line(3, 4, 3, 7)
		p.line(13, 4, 13, 7)
		p.line(3, 7, 13, 7)
		p.line(3, 9.5, 13, 9.5)
		p.line(3, 12, 13, 12)
	},
	"jobs": func(p *iconPen) {
		p.line(2.5, 4, 10, 4)
		p.line(2.5, 8, 8, 8)
		p.line(2.5, 12, 10, 12)
		p.poly(false, 11, 6, 13.5, 8, 11, 10)
	},
	"prompts": func(p *iconPen) {
		p.poly(true, 3, 1.5, 10, 1.5, 13, 4.5, 13, 14.5, 3, 14.5)
		p.line(5.5, 7, 10.5, 7)
		p.line(5.5, 9.5, 10.5, 9.5)
		p.line(5.5, 12, 8.5, 12)
	},
	"mic": func(p *iconPen) {
		p.poly(true, 6, 2, 10, 2, 10, 9, 6, 9)
		p.curve(3.5, 7.5, 3.5, 11, 6, 12, 8, 12)
		p.curve(8, 12, 10, 12, 12.5, 11, 12.5, 7.5)
		p.line(8, 12, 8, 14.5)
	},
	"folder": func(p *iconPen) {
		p.poly(true, 2, 3.5, 6.5, 3.5, 8, 5, 14, 5, 14, 13, 2, 13)
		p.line(2, 7, 14, 7)
	},
	"key": func(p *iconPen) {
		p.circle(5, 8, 3)
		p.line(8, 8, 14.5, 8)
		p.line(12, 8, 12, 10.5)
		p.line(14.5, 8, 14.5, 10.5)
	},
	"inbox": func(p *iconPen) {
		p.poly(true, 2, 2.5, 14, 2.5, 14, 13.5, 2, 13.5)
		p.poly(false, 2, 9, 5.5, 9, 6.5, 11, 9.5, 11, 10.5, 9, 14, 9)
	},
	"network": func(p *iconPen) {
		p.poly(false, 2, 5.5, 13, 5.5)
		p.poly(false, 10.5, 3, 13, 5.5, 10.5, 8)
		p.poly(false, 14, 10.5, 3, 10.5)
		p.poly(false, 5.5, 8, 3, 10.5, 5.5, 13)
	},
	"gateway": func(p *iconPen) {
		p.line(8, 14, 8, 2.5)
		p.poly(false, 4, 6.5, 8, 2.5, 12, 6.5)
	},
	"question": func(p *iconPen) {
		p.curve(5, 5.5, 5, 2, 11, 2, 11, 5.5)
		p.curve(11, 5.5, 11, 8, 8, 8, 8, 10.5)
		p.circle(8, 13.5, .6)
	},
	"permission": func(p *iconPen) {
		p.poly(true, 8, 1.5, 13.5, 3.5, 13.5, 8, 8, 14.5, 2.5, 8, 2.5, 3.5)
	},
	"mission": func(p *iconPen) {
		p.poly(true, 2, 2.5, 7, 2.5, 7, 7, 2, 7)
		p.poly(true, 9, 2.5, 14, 2.5, 14, 7, 9, 7)
		p.poly(true, 2, 9, 7, 9, 7, 13.5, 2, 13.5)
		p.poly(true, 9, 9, 14, 9, 14, 13.5, 9, 13.5)
	},
	"review": func(p *iconPen) {
		p.poly(false, 6, 2, 3, 2, 3, 14, 6, 14)
		p.poly(false, 10, 2, 13, 2, 13, 14, 10, 14)
		p.circle(8, 8, 1.6)
	},
}

func cos(a float64) float32 { return float32(math.Cos(a)) }
func sin(a float64) float32 { return float32(math.Sin(a)) }

// iconPen strokes an icon's 16-unit square into r.
type iconPen struct {
	f     render.Frame
	r     render.Rect
	style render.Style
}

func (p *iconPen) pt(x, y float32) render.Point {
	s := min(p.r.Dx(), p.r.Dy()) / 16
	return render.Point{X: p.r.Min.X + x*s, Y: p.r.Min.Y + y*s}
}

func (p *iconPen) line(x0, y0, x1, y1 float32) {
	var path render.Path
	path.MoveTo(p.pt(x0, y0)).LineTo(p.pt(x1, y1))
	p.f.Path(&path, p.style)
}

func (p *iconPen) curve(x0, y0, c1x, c1y, c2x, c2y, x1, y1 float32) {
	var path render.Path
	path.MoveTo(p.pt(x0, y0)).CubeTo(p.pt(c1x, c1y), p.pt(c2x, c2y), p.pt(x1, y1))
	p.f.Path(&path, p.style)
}

func (p *iconPen) poly(closed bool, xy ...float32) {
	var path render.Path
	path.MoveTo(p.pt(xy[0], xy[1]))
	for i := 2; i+1 < len(xy); i += 2 {
		path.LineTo(p.pt(xy[i], xy[i+1]))
	}
	if closed {
		path.Close()
	}
	p.f.Path(&path, p.style)
}

func (p *iconPen) circle(x, y, rad float32) {
	a, b := p.pt(x-rad, y-rad), p.pt(x+rad, y+rad)
	p.f.Path(render.Ellipse(render.Rect{Min: a, Max: b}), p.style)
}

// icon draws a named icon from the set, or name as a glyph, in a size-square box.
func icon(c *ui.Ctx, key any, name string, size float32, col render.Color) {
	if draw, ok := icons[name]; ok {
		c.Paint(key, ui.Px(size), ui.Px(size), func(f render.Frame, r render.Rect) {
			draw(&iconPen{f: f, r: r, style: render.Style{Stroke: col, Width: max(1.25, size/11)}})
		})
		return
	}
	if img := contributedIcon(name, col); img != nil {
		widget.Image(c, key, img, size, widget.ImageOpts{Label: name})
		return
	}
	c.Box(key, ui.Style{W: ui.Px(size), H: ui.Px(size), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("glyph", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size * .85, Weight: typeset.SemiBold}, Color: col})
	})
}

// iconReg is the registry contributed icons are found in: the window's.
var iconReg atomic.Pointer[registry.Registry]

// contributedIcon is the icon a plugin's [contributes] icons brings by name, as its shape in col: the image's own
// alpha, so a monochrome icon takes the tone around it. Nil when there's none, or it isn't a raster image.
func contributedIcon(name string, col render.Color) image.Image {
	r := iconReg.Load()
	if r == nil || *r == nil {
		return nil
	}
	var ic registry.Icon
	found := false
	for _, c := range (*r).Contributions("icon") {
		if i := c.(registry.Icon); i.Name == name {
			ic, found = i, true
		}
	}
	if !found {
		return nil
	}
	key := fmt.Sprintf("icon:%s/%s:%v", ic.Source, ic.Name, col)
	pictures.mu.Lock()
	pc, ok := pictures.m[key]
	pictures.mu.Unlock()
	if !ok {
		pc = &picture{}
		b := ic.Data
		if b == nil {
			b, pc.err = os.ReadFile(ic.Path)
		}
		if pc.err == nil {
			var src image.Image
			if src, pc.format, pc.err = decodeImage(b); pc.err == nil {
				pc.img = tinted(src, col)
			}
		}
		pictures.put(key, pc)
	}
	if pc.err != nil {
		return nil
	}
	return pc.img
}

// tinted is src's shape in col: col at each pixel, as opaque as src is there.
func tinted(src image.Image, col render.Color) image.Image {
	b := src.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			_, _, _, a := src.At(x, y).RGBA()
			i := out.PixOffset(x-b.Min.X, y-b.Min.Y)
			out.Pix[i], out.Pix[i+1], out.Pix[i+2], out.Pix[i+3] = col.R, col.G, col.B, uint8(a>>8*uint32(col.A)/255)
		}
	}
	return out
}
