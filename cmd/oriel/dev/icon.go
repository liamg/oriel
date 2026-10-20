package dev

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/scenes"
	"github.com/liamg/oriel/theme"
)

// iconSizes are a macOS iconset's sizes in points; each comes at 1x and 2x.
var iconSizes = []int{16, 32, 128, 256, 512}

// iconUnits is the logical size the icon is laid out at, then scaled to each pixel size.
const iconUnits = 128

// Icons renders the app icon (app.Icon) on the CPU into dir as a macOS iconset, for iconutil, in the built-in
// theme's variant v.
func Icons(dir string, v theme.Variant) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	th, err := scenes.Builtin(v)
	if err != nil {
		return err
	}
	for _, pt := range iconSizes {
		for _, k := range []int{1, 2} {
			name := fmt.Sprintf("icon_%dx%d.png", pt, pt)
			if k == 2 {
				name = fmt.Sprintf("icon_%dx%d@2x.png", pt, pt)
			}
			if err := writePNG(filepath.Join(dir, name), icon(th, pt*k)); err != nil {
				return err
			}
		}
	}
	return nil
}

// icon is the app icon at px×px, transparent outside its rounded square.
func icon(th *theme.Resolved, px int) *image.RGBA {
	scale := float32(px) / iconUnits
	var l render.List
	app.Icon(l.Record(scale), render.XYWH(0, 0, iconUnits, iconUnits), th)
	return raster.Render(&l, render.Point{X: iconUnits, Y: iconUnits}, scale, color.NRGBA{})
}
