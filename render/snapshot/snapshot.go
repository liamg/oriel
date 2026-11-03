// Package snapshot pins what the UI draws: display lists as text, and pixels as PNGs compared
// perceptually (deltaE in OkLab), against golden files or the mockups.
package snapshot

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// Update rewrites golden files instead of checking them: ORIEL_UPDATE=1 go test ./...
func Update() bool { return os.Getenv("ORIEL_UPDATE") != "" }

// List writes a display list one op per line, stably, so a golden diff shows what moved.
func List(l *render.List) string {
	var b strings.Builder
	depth := 0
	for _, op := range l.Ops {
		if op.Kind == render.OpPop {
			depth = max(depth-1, 0)
		}
		b.WriteString(strings.Repeat("  ", depth))
		switch op.Kind {
		case render.OpRect:
			fmt.Fprintf(&b, "rect %s%s", rect(op.Rect), style(op.Style))
		case render.OpPath:
			fmt.Fprintf(&b, "path %d segments%s", len(op.Path.Segs), style(op.Style))
		case render.OpGlyphs:
			fmt.Fprintf(&b, "text %s @%s,%s %s %s %s", strconv.Quote(op.Run.Text), num(op.At.X), num(op.At.Y), hex(op.Color), op.Run.Font.Family, num(op.Run.Font.Size))
		case render.OpImage:
			fmt.Fprintf(&b, "image %dx%d @%s,%s", op.Image.Rect.Dx(), op.Image.Rect.Dy(), num(op.At.X), num(op.At.Y))
		case render.OpEffect:
			fmt.Fprintf(&b, "effect %s %s blur %s %s", [...]string{"glow", "glow-disc"}[op.Effect], rect(op.Rect), num(op.Amount), hex(op.Color))
		case render.OpPushClip:
			fmt.Fprintf(&b, "clip %s r %s", rect(op.Rect), num(op.Amount))
			depth++
		case render.OpPushOpacity:
			fmt.Fprintf(&b, "opacity %s", num(op.Amount))
			depth++
		case render.OpPop:
			b.WriteString("pop")
		case render.OpBackdrop:
			fmt.Fprintf(&b, "backdrop %s", hex(op.Color))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func style(s render.Style) string {
	var b strings.Builder
	if g := s.Gradient; g != nil {
		dir := "down"
		if g.Horizontal {
			dir = "across"
		}
		fmt.Fprintf(&b, " gradient %s %s→%s", dir, hex(g.From), hex(g.To))
	} else if s.Fill.A > 0 {
		fmt.Fprintf(&b, " fill %s", hex(s.Fill))
	}
	if s.Radius > 0 {
		fmt.Fprintf(&b, " radius %s", num(s.Radius))
	}
	if s.Width > 0 && s.Stroke.A > 0 {
		fmt.Fprintf(&b, " stroke %s %s", num(s.Width), hex(s.Stroke))
	}
	return b.String()
}

func rect(r render.Rect) string {
	return fmt.Sprintf("%s,%s %sx%s", num(r.Min.X), num(r.Min.Y), num(r.Dx()), num(r.Dy()))
}

func num(v float32) string { return strconv.FormatFloat(float64(v), 'f', -1, 32) }

func hex(c color.NRGBA) string { return theme.Hex(c) }

// Golden compares got with the file at path, or writes it when updating. Numbers within float32 rounding of each
// other match: the last digit of a float32 can differ between platforms.
func Golden(t testing.TB, path string, got []byte) {
	t.Helper()
	if Update() {
		write(t, path, got)
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (ORIEL_UPDATE=1 writes it)", err)
	}
	if !bytes.Equal(got, want) && !sameText(string(want), string(got)) {
		t.Errorf("%s differs:\n%s", path, lineDiff(string(want), string(got)))
	}
}

var number = regexp.MustCompile(`-?[0-9]+(\.[0-9]+)?`)

// sameText says a and b differ at most in numbers that are within float32 rounding of each other.
func sameText(a, b string) bool {
	if number.ReplaceAllString(a, "0") != number.ReplaceAllString(b, "0") {
		return false
	}
	na, nb := number.FindAllString(a, -1), number.FindAllString(b, -1)
	for i := range na {
		if na[i] == nb[i] {
			continue
		}
		x, err1 := strconv.ParseFloat(na[i], 64)
		y, err2 := strconv.ParseFloat(nb[i], 64)
		if err1 != nil || err2 != nil || math.Abs(x-y) > 1e-6*max(1, math.Abs(x)) {
			return false
		}
	}
	return true
}

func write(t testing.TB, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// lineDiff shows the first lines that differ, with a little context.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)) && len(out) < 20; i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			out = append(out, fmt.Sprintf("%5d - %s", i+1, a), fmt.Sprintf("%5d + %s", i+1, b))
		}
	}
	return strings.Join(out, "\n")
}

// Diff is how far one image is from another, perceptually.
type Diff struct {
	Mean float64 // mean deltaE OK per pixel
	Off  float64 // fraction of pixels further apart than the threshold
}

func (d Diff) String() string {
	return fmt.Sprintf("mean ΔE %.4f, %.2f%% of pixels past the threshold", d.Mean, 100*d.Off)
}

// Compare measures got against want over the area they share: deltaE OK per pixel (alpha composited over
// black), the fraction past threshold, and want | got | heat map side by side.
func Compare(want, got image.Image, threshold float64) (Diff, *image.RGBA) {
	r := want.Bounds().Intersect(got.Bounds())
	w, h := r.Dx(), r.Dy()
	out := image.NewRGBA(image.Rect(0, 0, 3*w, h))
	lab := map[color.RGBA][3]float64{}
	toLab := func(c color.Color) [3]float64 {
		r, g, b, _ := c.RGBA()
		k := color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8)}
		if v, ok := lab[k]; ok {
			return v
		}
		o := theme.ToOKLCH(color.NRGBA{R: k.R, G: k.G, B: k.B, A: 255})
		hue := o.H
		if math.IsNaN(hue) {
			hue = 0
		}
		v := [3]float64{o.L, o.C * math.Cos(hue*math.Pi/180), o.C * math.Sin(hue*math.Pi/180)}
		if len(lab) < 1<<16 {
			lab[k] = v
		}
		return v
	}
	var sum float64
	var off int
	for y := range h {
		for x := range w {
			a, b := want.At(r.Min.X+x, r.Min.Y+y), got.At(r.Min.X+x, r.Min.Y+y)
			out.Set(x, y, a)
			out.Set(w+x, y, b)
			la, lb := toLab(a), toLab(b)
			d := math.Sqrt((la[0]-lb[0])*(la[0]-lb[0]) + (la[1]-lb[1])*(la[1]-lb[1]) + (la[2]-lb[2])*(la[2]-lb[2]))
			sum += d
			if d > threshold {
				off++
			}
			v := uint8(min(255, d*1000))
			out.Set(2*w+x, y, color.RGBA{R: v, G: v / 2, A: 255})
		}
	}
	n := float64(w * h)
	return Diff{Mean: sum / n, Off: float64(off) / n}, out
}

// GoldenPNG compares got with the PNG at path, failing when more than maxOff of its pixels are further than
// threshold (deltaE OK) from it; or writes it when updating. On failure it writes the comparison beside it.
func GoldenPNG(t testing.TB, path string, got image.Image, threshold, maxOff float64) Diff {
	t.Helper()
	if Update() {
		var b bytes.Buffer
		if err := png.Encode(&b, got); err != nil {
			t.Fatal(err)
		}
		write(t, path, b.Bytes())
		return Diff{}
	}
	want, err := ReadPNG(path)
	if err != nil {
		t.Fatalf("%v (ORIEL_UPDATE=1 writes it)", err)
	}
	if want.Bounds().Size() != got.Bounds().Size() {
		t.Fatalf("%s is %v, the render %v", path, want.Bounds().Size(), got.Bounds().Size())
	}
	d, side := Compare(want, got, threshold)
	if d.Off > maxOff {
		failed := strings.TrimSuffix(path, ".png") + ".failed.png"
		var b bytes.Buffer
		png.Encode(&b, side)
		os.WriteFile(failed, b.Bytes(), 0o644)
		t.Errorf("%s: %s; allowed %.2f%% (want | got | diff in %s)", path, d, 100*maxOff, failed)
	}
	return d
}

func ReadPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}
