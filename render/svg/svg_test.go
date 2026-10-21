package svg

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/typeset"
)

// badge is a badge in shields.io's flat style: a rounded clip, two coloured halves, a gradient sheen and text with a
// shadow, the text drawn at ten times its size under scale(.1).
const badge = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="90" height="20" role="img" aria-label="license: MIT">
<title>license: MIT</title>
<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/><stop offset="1" stop-opacity=".1"/></linearGradient>
<clipPath id="r"><rect width="90" height="20" rx="3" fill="#fff"/></clipPath>
<g clip-path="url(#r)"><rect width="51" height="20" fill="#555"/><rect x="51" width="39" height="20" fill="#007ec6"/><rect width="90" height="20" fill="url(#s)"/></g>
<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
<text aria-hidden="true" x="265" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)" textLength="410">license</text>
<text x="265" y="140" transform="scale(.1)" fill="#fff" textLength="410">license</text>
<text x="695" y="140" transform="scale(.1)" fill="#fff" textLength="290">MIT</text>
</g></svg>`

func draw(t *testing.T, d *Doc, scale float32) *image.RGBA {
	t.Helper()
	sh, err := typeset.NewBundled()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sh.Close)
	var l render.List
	d.Draw(l.Record(scale), render.Rect{Max: d.Size}, sh)
	return raster.Render(&l, d.Size, scale, color.NRGBA{})
}

func near(a color.RGBA, b color.NRGBA) bool {
	d := func(x, y uint8) bool { return math.Abs(float64(x)-float64(y)) <= 12 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
}

func TestBadge(t *testing.T) {
	d, err := Parse([]byte(badge))
	if err != nil {
		t.Fatal(err)
	}
	if d.Size != (render.Point{X: 90, Y: 20}) {
		t.Fatalf("size %v, want 90×20", d.Size)
	}
	img := draw(t, d, 2)
	if os.Getenv("SVG_OUT") != "" {
		f, _ := os.Create(os.Getenv("SVG_OUT"))
		png.Encode(f, img)
		f.Close()
	}
	grey, blue := color.NRGBA{0x55, 0x55, 0x55, 255}, color.NRGBA{0x00, 0x7e, 0xc6, 255}
	if c := img.RGBAAt(4, 20); !near(c, grey) {
		t.Errorf("left half %v, want about %v", c, grey)
	}
	if c := img.RGBAAt(176, 20); !near(c, blue) {
		t.Errorf("right half %v, want about %v", c, blue)
	}
	if c := img.RGBAAt(0, 0); c.A > 64 {
		t.Errorf("the rounded corner is filled: %v", c)
	}
	white := 0
	for y := 8; y < 32; y++ {
		for x := 0; x < 102; x++ {
			if c := img.RGBAAt(x, y); c.R > 200 && c.G > 200 && c.B > 200 {
				white++
			}
		}
	}
	if white < 40 {
		t.Errorf("the label's text drew %d white pixels", white)
	}
	for y := range 40 {
		if c := img.RGBAAt(102+2, y); c.R > 200 && c.G > 200 && c.B > 200 && y > 0 {
			t.Errorf("the label's text runs into the value at y %d", y)
			break
		}
	}
}

func TestViewBoxAndTransforms(t *testing.T) {
	d, err := Parse([]byte(`<svg viewBox="0 0 10 10" width="100">
		<g transform="translate(5 0)"><rect width="5" height="5" fill="rgb(255,0,0)"/></g>
		<circle cx="2.5" cy="7.5" r="2" style="fill:#00f"/>
		<path d="M5 5h5v5H5z" fill="lime" opacity="0.5"/>
		<rect width="5" height="5" fill="none" display="none"/>
	</svg>`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Size != (render.Point{X: 100, Y: 100}) {
		t.Fatalf("size %v, want 100×100 from the width and the viewBox's shape", d.Size)
	}
	img := draw(t, d, 1)
	for _, c := range []struct {
		x, y int
		want color.NRGBA
	}{
		{75, 25, color.NRGBA{255, 0, 0, 255}},
		{25, 25, color.NRGBA{}},
		{25, 75, color.NRGBA{0, 0, 255, 255}},
		{75, 75, color.NRGBA{0, 255, 0, 128}},
	} {
		got := img.RGBAAt(c.x, c.y)
		want := c.want
		if want.A < 255 {
			want = color.NRGBA{uint8(int(want.R) * int(want.A) / 255), uint8(int(want.G) * int(want.A) / 255), uint8(int(want.B) * int(want.A) / 255), want.A}
		}
		if !near(got, want) {
			t.Errorf("%d,%d = %v, want about %v", c.x, c.y, got, c.want)
		}
	}
}

// SVG 2, "Path data": numbers run together ("1.5.5" is two, "-1-2" two), flags take one digit, and a command repeats
// for further numbers (a moveto's as linetos).
func TestPathData(t *testing.T) {
	p := parsePath("M0,0 10-0 .5.5l1e1 0 h-5 v2 z m1 1 2 2 A5 5 0 0110 10")
	ops := ""
	for _, s := range p.Segs {
		ops += string("MLQCZ"[s.Op])
	}
	if !strings.HasPrefix(ops, "MLLLLLZMLC") || strings.Trim(ops[9:], "C") != "" {
		t.Fatalf("ops %s", ops)
	}
	if got := p.Segs[2].Pts[0]; got != (render.Point{X: .5, Y: .5}) {
		t.Errorf("'.5.5' read as %v", got)
	}
	if got := p.Segs[3].Pts[0]; got != (render.Point{X: 10.5, Y: .5}) {
		t.Errorf("relative 1e1 read as %v", got)
	}
	if last := p.Segs[len(p.Segs)-1]; last.Op != render.CubeTo || last.Pts[2] != (render.Point{X: 10, Y: 10}) {
		t.Errorf("the arc ends at %v, want 10,10", last.Pts[2])
	}
	if p := parsePath("L1 1"); len(p.Segs) != 0 {
		t.Errorf("path data not starting with a moveto drew %d segments", len(p.Segs))
	}
}

func TestColors(t *testing.T) {
	for s, want := range map[string]color.NRGBA{
		"#abc":                  {0xaa, 0xbb, 0xcc, 255},
		"#11223380":             {0x11, 0x22, 0x33, 0x80},
		"rgba(10, 20, 30, 0.5)": {10, 20, 30, 127},
		"rgb(100% 0% 0%)":       {255, 0, 0, 255},
		"SteelBlue":             {0x46, 0x82, 0xb4, 255},
	} {
		if got, ok := parseColor(s); !ok || got != want {
			t.Errorf("%s = %v, want %v", s, got, want)
		}
	}
}

func TestNotSVG(t *testing.T) {
	for _, s := range []string{"", "<html></html>", "\x89PNG"} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("%q parsed", s)
		}
	}
}
