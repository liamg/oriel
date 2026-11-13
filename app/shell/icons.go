package shell

import (
	"fmt"
	"image"

	"github.com/liamg/oriel/app/kit"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
)

// icon draws a named icon as kit.Icon does, from the window's registry (shell.provide).
func icon(c *ui.Ctx, key any, name string, size float32, col render.Color) {
	r, _ := ui.Service[registry.Registry](c)
	kit.Icon(c, r, key, name, size, col)
}

// contributedIcon is the icon a plugin's [contributes] icons in r brings by name, as its shape in col: the image's
// own alpha, so a monochrome icon takes the tone around it. Nil when there's none, or it isn't a raster image.
func contributedIcon(r registry.Registry, pictures *kit.Pictures, name string, col render.Color) image.Image {
	if r == nil {
		return nil
	}
	var ic registry.Icon
	found := false
	for _, i := range registry.ContributionsOf[registry.Icon](r) {
		if i.Name == name {
			ic, found = i, true
		}
	}
	if !found {
		return nil
	}
	key := fmt.Sprintf("icon:%s/%s:%v", ic.Source, ic.Name, col)
	var from any
	if len(ic.Data) > 0 {
		from = &ic.Data[0] // the bytes, by identity: a slice isn't comparable
	} else {
		file := pictures.File(ic.Path)
		if file.Img == nil {
			return nil
		}
		from = file.Img
	}
	pc := pictures.Memo(key, from, func() *kit.Picture {
		pc := &kit.Picture{}
		var src image.Image
		if img, ok := from.(image.Image); ok {
			src = img
		} else {
			src, pc.Format, pc.Err = kit.DecodeImage(ic.Data)
		}
		if pc.Err == nil {
			pc.Img = tinted(src, col)
		}
		return pc
	})
	if pc.Err != nil {
		return nil
	}
	return pc.Img
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
