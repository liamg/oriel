package dev

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/theme"
)

// An iconset is what iconutil takes: every size macOS asks for, at 1x and 2x, as PNGs of that many pixels.
func TestIconsMakeAnIconset(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "oriel.iconset")
	if err := Icons(dir, theme.Dark); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{
		"icon_16x16.png": 16, "icon_16x16@2x.png": 32, "icon_32x32.png": 32, "icon_32x32@2x.png": 64,
		"icon_128x128.png": 128, "icon_128x128@2x.png": 256, "icon_256x256.png": 256, "icon_256x256@2x.png": 512,
		"icon_512x512.png": 512, "icon_512x512@2x.png": 1024,
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Errorf("%d files, want %d", len(entries), len(want))
	}
	for name, px := range want {
		f, err := os.Open(filepath.Join(dir, name))
		if err != nil {
			t.Error(err)
			continue
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if b := img.Bounds(); b.Dx() != px || b.Dy() != px {
			t.Errorf("%s is %v, want %dx%d", name, b.Size(), px, px)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%s: the corner outside the rounded square isn't transparent", name)
		}
		if _, _, _, a := img.At(px/2, px/5).RGBA(); a != 0xffff {
			t.Errorf("%s: the square isn't opaque", name)
		}
	}
}
