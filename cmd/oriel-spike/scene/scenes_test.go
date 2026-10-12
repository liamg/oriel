package scene

import (
	"testing"

	"gioui.org/op"
)

func TestScenesDraw(t *testing.T) {
	sh, err := NewShaper(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range All() {
		for _, scale := range []float32{1, 1.25, 2} {
			var ops op.Ops
			c := NewCanvas(&ops, sh, scale)
			st := NewState()
			for i := range 3 {
				st.Frame, st.T, st.Line = i, float64(i)/4, FileLines+i
				ops.Reset()
				s.Draw(c, st)
			}
		}
	}
}

func TestBundledFonts(t *testing.T) {
	want := map[string]bool{string(Mono): false, string(UI): false, string(Display): false}
	c, err := Collection()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range c {
		want[string(f.Font.Typeface)] = true
	}
	for name, ok := range want {
		if !ok {
			t.Errorf("no bundled face for %q (have %v)", name, FaceNames())
		}
	}
}

func TestSRGBAlpha(t *testing.T) {
	// Over black, a·lin(c) blended in linear light must encode back to the sRGB-blended a·c.
	for _, a := range []float64{.07, .25, .55, 1} {
		got := srgbAlpha(a, 1)
		if want := lin(a); got < want-1e-9 || got > want+1e-9 {
			t.Errorf("srgbAlpha(%v, 1) = %v, want %v", a, got, want)
		}
	}
}
