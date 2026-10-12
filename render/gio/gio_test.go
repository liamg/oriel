package gio

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/scenes"
	"github.com/liamg/oriel/render/typeset"
)

// shots is where tests write what they drew, when ORIEL_SHOTS names a directory.
func shot(t *testing.T, name string, img image.Image) {
	dir := os.Getenv("ORIEL_SHOTS")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func readPNG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func drawScene(t *testing.T, s scenes.Scene, scale float32) *image.RGBA {
	t.Helper()
	ts, err := typeset.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	w, h := s.Size()
	hl, err := NewHeadless(w, h, scale)
	if err != nil {
		t.Fatalf("no offscreen GPU surface (Gio needs EGL/Mesa on Linux): %v", err)
	}
	defer hl.Release()
	c := scenes.NewCanvas(ts)
	st := scenes.NewState()
	if err := hl.Draw(func(f render.Frame) { c.Begin(f); s.Draw(c, st) }); err != nil {
		t.Fatal(err)
	}
	img, err := hl.Screenshot()
	if err != nil {
		t.Fatal(err)
	}
	return img
}

// 01-ide drawn through render.Frame on Gio, against the mockup it was read from. The spike measured
// 7.2/255 and 5.5% at 1×; most of the rest is symbol glyphs a system without symbol fonts lacks,
// fractional mono advances (Chrome rounds them) and the terminal pane's ligatures.
func TestIDEMatchesTheMockup(t *testing.T) {
	ide, _ := scenes.ByName("ide")
	got := drawScene(t, ide, 1)
	d, side := Compare(readPNG(t, "../../_design/mockups/01-ide.png"), got)
	shot(t, "ide-1x.png", got)
	shot(t, "ide-mockup-native-diff.png", side)
	t.Logf("01-ide @1×: mean abs diff %.2f/255, %.2f%% of pixels off by more than 32/255", d.Mean, 100*d.Off)
	if d.Mean > 8 || d.Off > 0.065 {
		t.Errorf("01-ide is %.2f/255 and %.2f%% off the mockup; the spike was 7.2 and 5.5%%", d.Mean, 100*d.Off)
	}
}

func TestEveryScaleDraws(t *testing.T) {
	for _, s := range scenes.All() {
		for _, scale := range []float32{1, 1.25, 1.5, 2} {
			img := drawScene(t, s, scale)
			w, h := s.Size()
			if b := img.Bounds(); b.Dx() != int(float32(w)*scale+0.5) || b.Dy() != int(float32(h)*scale+0.5) {
				t.Errorf("%s at %v× is %v", s.Name(), scale, b)
			}
		}
	}
}
