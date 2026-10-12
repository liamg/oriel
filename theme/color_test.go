package theme

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestSRGBToOKLCH(t *testing.T) {
	for _, tt := range []struct {
		name     string
		c        Color
		l, ch, h float64
	}{
		// ref:   css-color-4/Overview.bs#the-hsl-notation
		// quote: "In OkLCh, sRGB blue is oklch(0.452 0.313 264.1) while sRGB yellow is oklch(0.968 0.211 109.8)."
		{"blue", rgba(0, 0, 255, 255), 0.452, 0.313, 264.1},
		{"yellow", rgba(255, 255, 0, 255), 0.968, 0.211, 109.8},
	} {
		o := ToOKLCH(tt.c)
		if !near(o.L, tt.l, 0.0005) || !near(o.C, tt.ch, 0.0005) || !near(o.H, tt.h, 0.05) {
			t.Errorf("%s: %+v, want oklch(%v %v %v)", tt.name, o, tt.l, tt.ch, tt.h)
		}
		if back := FromOKLCH(o); back != tt.c {
			t.Errorf("%s: round trip gave %s", tt.name, Hex(back))
		}
	}
	// ref:   css-color-4/conversions.js#OKLab_to_OKLCH
	// quote: "if (chroma <= epsilon) { hue = NaN; }"
	if o := ToOKLCH(rgba(128, 128, 128, 255)); !math.IsNaN(o.H) {
		t.Errorf("grey has hue %v; it's powerless", o.H)
	}
}

func TestMixTakesTheShorterArc(t *testing.T) {
	// ref:   css-color-4/Overview.bs#hue-shorter
	// quote: "the midpoint when interpolating in OkLCh from a red oklch(0.6 0.24 30) to a yellow oklch(0.8 0.15 90) would be at a hue angle of 30 + (90 - 30) * 0.5 = 60 degrees, along the shorter arc between the two colors, giving a deep orange oklch(0.7 0.195 60)"
	got := MixOKLCH(OKLCH{0.6, 0.24, 30, 1}, OKLCH{0.8, 0.15, 90, 1}, 0.5)
	if !near(got.L, 0.7, 1e-9) || !near(got.C, 0.195, 1e-9) || !near(got.H, 60, 1e-9) {
		t.Errorf("midpoint %+v, want oklch(0.7 0.195 60)", got)
	}
	// Across 0°: 350° and 10° meet at 0, not 180.
	if got := MixOKLCH(OKLCH{0.5, 0.1, 350, 1}, OKLCH{0.5, 0.1, 10, 1}, 0.5); !near(math.Mod(got.H+360, 360), 0, 1e-9) {
		t.Errorf("350° to 10° midpoint at %v", got.H)
	}
	if got := Mix(rgba(0x06, 0x06, 0x06, 255), rgba(0xff, 0xd2, 0x3f, 255), 0); got != rgba(0x06, 0x06, 0x06, 255) {
		t.Errorf("0%% mix changed the colour: %s", Hex(got))
	}
}

func TestGamutMapping(t *testing.T) {
	// ref:   css-color-4/Overview.bs#GMA-Binary-local-MINDE
	// quote: "For the OkLCh color space, one JND is is an OkLCh difference of 0.02."
	for _, o := range []OKLCH{{0.7, 0.4, 30, 1}, {0.9, 0.3, 140, 1}, {0.3, 0.35, 270, 1}} {
		if inGamut(lchToSRGB(o)) {
			t.Fatalf("%+v is meant to be out of gamut", o)
		}
		rgb := gamutMap(o)
		if !inGamut(rgb) {
			t.Errorf("%+v mapped out of gamut: %v", o, rgb)
		}
		mapped := labToLCH(srgbToLab(rgb), 1)
		if !near(mapped.L, o.L, 0.03) {
			t.Errorf("%+v: lightness moved to %v; mapping reduces chroma", o, mapped.L)
		}
		if mapped.C >= o.C {
			t.Errorf("%+v: chroma %v didn't go down", o, mapped.C)
		}
	}
	// ref:   css-color-4/Overview.bs#pseudo-binsearch
	// quote: "if the Lightness of origin_OkLCh is greater than or equal to 100%, convert oklab(1 0 0 / origin.alpha) to destination and return it as the gamut mapped color"
	if c := FromOKLCH(OKLCH{1.2, 0.2, 40, 1}); c != rgba(255, 255, 255, 255) {
		t.Errorf("L above 1 maps to %s, want white", Hex(c))
	}
}

func TestParseColor(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want Color
	}{
		{"#ffd23f", rgba(0xff, 0xd2, 0x3f, 255)},
		{"#FFD23F80", rgba(0xff, 0xd2, 0x3f, 0x80)},
		{"#fa0", rgba(0xff, 0xaa, 0x00, 255)},
		{"rgb(255 210 63)", rgba(0xff, 0xd2, 0x3f, 255)},
		{"rgba(255, 210, 63, .08)", rgba(0xff, 0xd2, 0x3f, 20)},
		{"rgb(100% 0% 0% / 50%)", rgba(255, 0, 0, 128)},
		{"oklch(0.452 0.313 264.1)", rgba(0, 0, 255, 255)}, // the spec's rounded figures for blue land within a step
		{"oklch(45.2% 0.313 264.1deg)", rgba(0, 0, 255, 255)},
	} {
		got, err := ParseColor(tt.in)
		if err != nil || !within1(got, tt.want) {
			t.Errorf("ParseColor(%q) = %s, %v; want %s", tt.in, Hex(got), err, Hex(tt.want))
		}
	}
	for _, bad := range []string{"", "#ff", "#gggggg", "rgb(1 2)", "oklch(1 2)", "teal"} {
		if _, err := ParseColor(bad); err == nil {
			t.Errorf("ParseColor(%q) succeeded", bad)
		}
	}
}

func TestOverAndLighten(t *testing.T) {
	// 8% of the accent over the surface, blended as CSS does.
	if got := Over(rgba(0xff, 0xd2, 0x3f, 20), rgba(0x0c, 0x0c, 0x0b, 255)); got != rgba(0x1f, 0x1c, 0x0f, 255) {
		t.Errorf("Over = %s", Hex(got))
	}
	base := rgba(0x60, 0x60, 0x60, 255)
	if l := ToOKLCH(Lighten(base, 0.1)).L - ToOKLCH(base).L; !near(l, 0.1, 0.003) {
		t.Errorf("lighten by 0.1 moved L by %v", l)
	}
}

func within1(a, b Color) bool {
	d := func(x, y uint8) bool { return x-y <= 1 || y-x <= 1 }
	return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
}
