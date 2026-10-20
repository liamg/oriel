package scenes

import (
	"image"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

func record(t *testing.T, v theme.Variant, s Scene, scale float32) *render.List {
	t.Helper()
	th, err := Builtin(v)
	if err != nil {
		t.Fatal(err)
	}
	return recordIn(t, th, s, scale)
}

func recordIn(t *testing.T, th *theme.Resolved, s Scene, scale float32) *render.List {
	t.Helper()
	ts, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	var l render.List
	c := NewCanvas(ts, th)
	c.Begin(l.Record(scale))
	s.Draw(c, NewState())
	return &l
}

func rasterise(t *testing.T, v theme.Variant, name string, scale float32) *image.RGBA {
	t.Helper()
	s, _ := ByName(name)
	w, h := s.Size()
	th, _ := Builtin(v)
	return raster.Render(record(t, v, s, scale), render.Point{X: float32(w), Y: float32(h)}, scale, th.C(theme.BgBase))
}

// The fixture workspace (01-ide) drawn on the CPU against the mockups it reproduces, in each built-in variant.
// A pixel counts as off past deltaE OK 0.1, well past one JND (0.02): what's left is glyphs this machine has no
// font for, Chrome's rounded mono advances and the terminal's ligatures (spikes/P0-4-gio.md).
func TestFixtureAgainstTheMockups(t *testing.T) {
	for _, tt := range []struct {
		v      theme.Variant
		mockup string
		mean   float64
		off    float64
	}{
		{theme.Dark, "01-ide.png", 0.025, 0.06},
		{theme.Light, "17-light.png", 0.025, 0.06},
	} {
		want, err := snapshot.ReadPNG(filepath.Join("..", "..", "_design", "mockups", tt.mockup))
		if err != nil {
			t.Fatal(err)
		}
		d, _ := snapshot.Compare(want, rasterise(t, tt.v, "ide", 1), 0.1)
		t.Logf("%s against %s: %s", tt.v, tt.mockup, d)
		if d.Mean > tt.mean || d.Off > tt.off {
			t.Errorf("%s: %s; allowed mean %v and %.0f%%", tt.v, d, tt.mean, 100*tt.off)
		}
	}
}

// Goldens of the fixture as drawn: any change to what it draws shows up here first, as a display-list diff,
// then as pixels (anything past one JND on more than 0.1% of them), in every built-in theme at each variant it has.
func TestFixtureGoldens(t *testing.T) {
	ide, _ := ByName("ide")
	snapshot.Golden(t, "testdata/ide-dark.list", []byte(snapshot.List(record(t, theme.Dark, ide, 1))))
	for _, src := range theme.Builtins() {
		st, err := theme.Stack(theme.Builtins(), src.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range slices.Compact([]theme.Variant{theme.Effective(st, theme.Dark), theme.Effective(st, theme.Light)}) {
			th, err := theme.Resolve(st, v)
			if err != nil {
				t.Fatalf("%s %s: %v", src.Name(), v, err)
			}
			img := raster.Render(recordIn(t, th, ide, 1), render.Point{X: IDEWidth, Y: IDEHeight}, 1, th.C(theme.BgBase))
			snapshot.GoldenPNG(t, "testdata/ide-"+src.Name()+"-"+string(v)+".png", img, 0.02, 0.001)
		}
	}
	snapshot.GoldenPNG(t, "testdata/ide-dark@2x.png", rasterise(t, theme.Dark, "ide", 2), 0.02, 0.001)
}
