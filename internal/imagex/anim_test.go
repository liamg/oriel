package imagex

import (
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func animation(t *testing.T, name string) *Animation {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	a, err := Animate(b)
	if err != nil || a == nil {
		t.Fatalf("%s: %v, %v", name, a, err)
	}
	return a
}

// lossy is how far a lossy frame's channel may be from ImageMagick's: x/image/webp's YCbCr conversion and libwebp's
// differ by up to about 16 on a tiny frame.
const lossy = 20

// near says c is want within tol a channel: lossy frames come back close, not exact.
func near(c, want color.RGBA, tol int) bool {
	d := func(a, b uint8) bool { return int(a)-int(b) <= tol && int(b)-int(a) <= tol }
	return d(c.R, want.R) && d(c.G, want.G) && d(c.B, want.B) && d(c.A, want.A)
}

var (
	red  = color.RGBA{R: 255, A: 255}
	blue = color.RGBA{B: 255, A: 255}
	lime = color.RGBA{G: 255, A: 255}
	none = color.RGBA{}
)

// Each frame is the canvas as it's shown: a small frame is drawn at its place over what the frames before left
// there, and shows for its delay. The fixtures' frames: 8×8 red; 4×4 blue at (2,2); 2×2 lime at (0,0), none
// disposed. Expected pixels and delays are ImageMagick's (`magick anim.gif -coalesce`).
//
// ref:   gif/spec-gif89a.txt
// quote: "1 -   Do not dispose. The graphic is to be left in place."
// ref:   webp/rfc9649.txt
// quote: "The rendering of the decoded frame begins at the Cartesian coordinates (2 * Frame X, 2 * Frame Y), using the top-left corner of the canvas as the origin."
func TestAnimationsComposeTheirFrames(t *testing.T) {
	for _, name := range []string{"anim.gif", "anim.webp"} {
		a := animation(t, name)
		want := []struct {
			delay              time.Duration
			p00, p33, p55, p77 color.RGBA
		}{
			{100 * time.Millisecond, red, red, red, red},
			{200 * time.Millisecond, red, blue, blue, red},
			{300 * time.Millisecond, lime, blue, blue, red},
		}
		if len(a.Frames) != len(want) || a.Loops != 0 {
			t.Fatalf("%s: %d frames looping %d times, want 3 forever", name, len(a.Frames), a.Loops)
		}
		for i, w := range want {
			f := a.Frames[i]
			if f.Rect.Dx() != 8 || f.Rect.Dy() != 8 {
				t.Errorf("%s frame %d is %v, want the 8×8 canvas", name, i, f.Rect)
			}
			for _, p := range []struct {
				x, y int
				c    color.RGBA
			}{{0, 0, w.p00}, {3, 3, w.p33}, {5, 5, w.p55}, {7, 7, w.p77}} {
				if got := f.RGBAAt(p.x, p.y); got != p.c {
					t.Errorf("%s frame %d at (%d,%d) is %v, want %v", name, i, p.x, p.y, got, p.c)
				}
			}
			if a.Delays[i] != w.delay {
				t.Errorf("%s frame %d shows for %v, want %v", name, i, a.Delays[i], w.delay)
			}
		}
	}
}

// A lossy (VP8) frame decodes inside its ANMF chunk too, and a canvas no frame covers stays transparent. Expected
// pixels are ImageMagick's.
//
// ref:   webp/rfc9649.txt
// quote: "Consists of: *  An optional alpha subchunk (Section 2.7.1.2) for the frame. *  A bitstream subchunk (Section 2.7.1.3) for the frame."
func TestLossyWebPFrames(t *testing.T) {
	a := animation(t, "lossy.webp")
	if len(a.Frames) != 2 {
		t.Fatalf("%d frames, want 2", len(a.Frames))
	}
	if c := a.Frames[0].RGBAAt(0, 0); c != none {
		t.Errorf("the uncovered canvas is %v, want transparent", c)
	}
	if c := a.Frames[0].RGBAAt(5, 5); !near(c, red, lossy) {
		t.Errorf("frame 0 at (5,5) is %v, want red", c)
	}
	if c := a.Frames[1].RGBAAt(3, 3); !near(c, blue, lossy) {
		t.Errorf("frame 1 at (3,3) is %v, want blue", c)
	}
}

// A frame's ALPH chunk carries its transparency: the bottom half of each frame is half see-through. Expected pixels
// are ImageMagick's: (6,6) is (0,100·i,255) at alpha 128, premultiplied here.
//
// ref:   webp/rfc9649.txt
// quote: "Alpha (L): 1 bit Set if any of the frames of the image contain transparency information (\"alpha\")."
func TestWebPFramesKeepTheirAlpha(t *testing.T) {
	a := animation(t, "alpha.webp")
	if len(a.Frames) != 3 {
		t.Fatalf("%d frames, want 3", len(a.Frames))
	}
	if c := a.Frames[0].RGBAAt(0, 0); !near(c, red, lossy) {
		t.Errorf("frame 0's opaque top is %v, want red", c)
	}
	if c := a.Frames[1].RGBAAt(7, 7); !near(c, color.RGBA{G: 50, B: 128, A: 128}, lossy) {
		t.Errorf("frame 1's half see-through bottom is %v, want (0,50,128,128) premultiplied", c)
	}
}

// The loop count says how many times it plays: GIF's NETSCAPE count repeats it, WebP's plays it, 0 for forever.
// Past its last loop it stays on its last frame.
//
// ref:   webp/rfc9649.txt
// quote: "The number of times to loop the animation.  If it is 0, this means infinitely."
func TestAnimationLoops(t *testing.T) {
	a := animation(t, "lossy.webp")
	if a.Loops != 2 {
		t.Fatalf("loops %d, want 2", a.Loops)
	}
	if img, next := a.At(50 * time.Millisecond); img != a.Frames[0] || next != 50*time.Millisecond {
		t.Errorf("50ms in: frame %p, next in %v; want frame 0, next in 50ms", img, next)
	}
	if img, _ := a.At(350 * time.Millisecond); img != a.Frames[0] {
		t.Error("the second loop doesn't start over")
	}
	if img, next := a.At(time.Second); img != a.Frames[1] || next != 0 {
		t.Errorf("past both loops: next in %v, want it stopped on the last frame", next)
	}
	if forever := animation(t, "anim.gif"); forever.Loops != 0 {
		t.Errorf("anim.gif loops %d times, want forever", forever.Loops)
	}
}

// A frame with a delay of 10ms or less shows for 100ms, as browsers draw it.
//
// ref:   webp/rfc9649.txt
// quote: "Note that the interpretation of the Frame Duration of 0 (and often <= 10) is defined by the implementation."
func TestBriefFramesSlowDown(t *testing.T) {
	if d := delay(0); d != minDelay {
		t.Errorf("0 → %v", d)
	}
	if d := delay(20 * time.Millisecond); d != 20*time.Millisecond {
		t.Errorf("20ms → %v", d)
	}
}

// A still picture, or one frame, isn't an animation.
func TestStillsAreNotAnimations(t *testing.T) {
	b, err := os.ReadFile("testdata/still.webp")
	if err != nil {
		t.Fatal(err)
	}
	if a, err := Animate(b); a != nil || err != nil {
		t.Errorf("still.webp: %v, %v", a, err)
	}
}

// An animated WebP, which x/image/webp won't decode, shows its first frame wherever a still is drawn.
func TestAnimatedWebPDecodesAsItsFirstFrame(t *testing.T) {
	b, err := os.ReadFile("testdata/anim.webp")
	if err != nil {
		t.Fatal(err)
	}
	img, format, err := Decode(b)
	if err != nil || format != "webp" || img.Bounds().Dx() != 8 {
		t.Fatalf("Decode: %v, %q, %v", img, format, err)
	}
	if _, _, a, err := DecodeAnimated(b); err != nil || a == nil || len(a.Frames) != 3 {
		t.Errorf("DecodeAnimated: %v, %v", a, err)
	}
}
