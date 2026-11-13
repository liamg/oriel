package gio

import (
	"image"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/typeset"
)

// Colour emoji draw on Gio as the CPU renderer draws them, in their colours: a sequence, a flag, a keycap and the
// newest emoji beside symbols set in the text's weight.
func TestColourEmojiDrawAsTheCPUDrawsThem(t *testing.T) {
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	run := ts.Shape("ok ✅ 🚀 👍🏽 👨‍💻 🇬🇧 1️⃣ 🫩 ★ ⏺ ⌘", typeset.FontSpec{Family: typeset.Mono, Size: 20})
	bg := render.Color{R: 0x14, G: 0x15, B: 0x1a, A: 0xff}
	fg := render.Color{R: 0xec, G: 0xe6, B: 0xd8, A: 0xff}
	const w, h = 420, 48
	draw := func(f render.Frame) {
		f.Rect(render.Rect{Max: render.Point{X: w, Y: h}}, render.Style{Fill: bg})
		f.Glyphs(run, render.Point{X: 8, Y: 32}, fg)
	}
	hl, err := NewHeadless(w, h, 2)
	if err != nil {
		t.Fatalf("no offscreen GPU surface (Gio needs EGL/Mesa on Linux): %v", err)
	}
	defer hl.Release()
	if err := hl.Draw(draw); err != nil {
		t.Fatal(err)
	}
	got, err := hl.Screenshot()
	if err != nil {
		t.Fatal(err)
	}
	cpu := raster.New(image.Pt(w*2, h*2), 2)
	draw(cpu)
	d, side := Compare(cpu.Result(), got)
	shot(t, "emoji-gio.png", got)
	shot(t, "emoji-cpu-gio-diff.png", side)
	t.Logf("mean abs diff %.2f/255, %.2f%% of pixels off by more than 32/255", d.Mean, 100*d.Off)
	if d.Mean > 2 || d.Off > 0.01 {
		t.Errorf("Gio's emoji are %.2f/255 and %.2f%% off the CPU's", d.Mean, 100*d.Off)
	}
	coloured := 0
	b := got.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := got.RGBAAt(x, y)
			if max(c.R, c.G, c.B)-min(c.R, c.G, c.B) > 80 {
				coloured++
			}
		}
	}
	if coloured < 500 {
		t.Errorf("only %d coloured pixels: the emoji aren't drawn in colour", coloured)
	}
}
