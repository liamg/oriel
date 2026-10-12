//go:build spike

package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gioui.org/gpu/headless"
	"gioui.org/op"

	"github.com/liamg/oriel/cmd/oriel-spike/scene"
)

// render draws one frame of s headless at the given scale.
func render(s scene.Scene, st *scene.State, scale float32) (*image.RGBA, error) {
	w, h := s.Size()
	pw, ph := int(float32(w)*scale), int(float32(h)*scale)
	win, err := headless.NewWindow(pw, ph)
	if err != nil {
		return nil, err
	}
	defer win.Release()
	sh, err := scene.NewFonts()
	if err != nil {
		return nil, err
	}
	var ops op.Ops
	c := scene.NewCanvas(&ops, sh, scale)
	c.Mode, c.BakedShapes = textMode, bakedShapes
	s.Draw(c, st)
	if err := win.Frame(&ops); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, pw, ph))
	return img, win.Screenshot(img)
}

func shots(dir, scales string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, sc := range strings.Split(scales, ",") {
		scale, err := strconv.ParseFloat(sc, 32)
		if err != nil {
			return err
		}
		for _, s := range scene.All() {
			img, err := render(s, scene.NewState(), float32(scale))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name(), err)
			}
			name := filepath.Join(dir, fmt.Sprintf("%s@%sx.png", s.Name(), sc))
			if err := writePNG(name, img); err != nil {
				return err
			}
			fmt.Println(name)
		}
	}
	return nil
}

func writePNG(name string, img image.Image) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
