// Package dev is `oriel dev`: screenshots, mockup comparisons and the shell's budget measurements, run against
// render/gio with the reference scenes.
package dev

import (
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/gio"
	"github.com/liamg/oriel/render/scenes"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
)

var started = time.Now()

// Shots renders every scene offscreen at each scale into dir, as <scene>@<scale>x.png, in the built-in
// theme's variant v.
func Shots(dir, scales string, v theme.Variant) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ts, err := typeset.New(nil)
	if err != nil {
		return err
	}
	th, err := scenes.Builtin(v)
	if err != nil {
		return err
	}
	for _, sc := range strings.Split(scales, ",") {
		scale, err := strconv.ParseFloat(sc, 32)
		if err != nil {
			return err
		}
		for _, s := range scenes.All() {
			img, err := shot(ts, th, s, float32(scale))
			if err != nil {
				return fmt.Errorf("%s: %w", s.Name(), err)
			}
			if err := writePNG(filepath.Join(dir, fmt.Sprintf("%s@%sx.png", s.Name(), sc)), img); err != nil {
				return err
			}
		}
	}
	return nil
}

func shot(ts *typeset.Typesetter, th *theme.Resolved, s scenes.Scene, scale float32) (*image.RGBA, error) {
	w, h := s.Size()
	hl, err := gio.NewHeadless(w, h, scale)
	if err != nil {
		return nil, err
	}
	defer hl.Release()
	c := scenes.NewCanvas(ts, th)
	st := scenes.NewState()
	if err := hl.Draw(func(f render.Frame) { c.Begin(f); s.Draw(c, st) }); err != nil {
		return nil, err
	}
	return hl.Screenshot()
}

// Compare writes mockup | native | diff side by side to out and prints how far apart they are.
func Compare(out io.Writer, mockup, native, dst string) error {
	a, err := readPNG(mockup)
	if err != nil {
		return err
	}
	b, err := readPNG(native)
	if err != nil {
		return err
	}
	d, side := gio.Compare(a, b)
	fmt.Fprintf(out, "mean abs diff %.2f/255, %.2f%% of pixels differ by more than 32/255\n", d.Mean, 100*d.Off)
	return writePNG(dst, side)
}

func readPNG(name string) (image.Image, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
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

// record draws a scene the way the app does: into a display list, then replayed into the backend.
func record(c *scenes.Canvas, s scenes.Scene, st *scenes.State, f render.Frame) {
	var l render.List
	c.Begin(l.Record(f.Scale()))
	s.Draw(c, st)
	l.Replay(f)
}

// Bench measures each scene offscreen: the CPU time to build a frame, Gio's time to encode and
// submit it, and the GPU's time per frame when frames are submitted back to back.
func Bench(out io.Writer, only string) error {
	const frames = 300
	fmt.Fprintf(out, "RSS at start: %.0f MB\n", currentRSS())
	ts, err := typeset.New(nil)
	if err != nil {
		return err
	}
	hl, err := gio.NewHeadless(scenes.IDEWidth, scenes.IDEHeight, 1)
	if err != nil {
		return err
	}
	defer hl.Release()
	fmt.Fprintf(out, "RSS with the GPU context: %.0f MB\n", currentRSS())
	th, err := scenes.Builtin(theme.Dark)
	if err != nil {
		return err
	}
	c := scenes.NewCanvas(ts, th)
	ide, _ := scenes.ByName("ide")
	st := scenes.NewState()
	if err := hl.Draw(func(f render.Frame) { record(c, ide, st, f) }); err != nil {
		return err
	}
	if err := hl.Sync(); err != nil {
		return err
	}
	fmt.Fprintf(out, "startup: first ide frame drawn %s after start (offscreen)\n", time.Since(started).Round(time.Millisecond))
	fmt.Fprintf(out, "RSS after the first ide frame: %.0f MB now, %.0f MB peak\n", currentRSS(), peakRSS())
	fmt.Fprintf(out, "%s %s/%s, %d frames per scene at %d×%d @1×\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, frames, scenes.IDEWidth, scenes.IDEHeight)
	fmt.Fprintln(out, "build: record the scene and replay it into Gio ops. frame(): Gio's CPU work to encode and submit.")
	fmt.Fprintln(out, "pipelined: frames submitted back to back, one sync per 30: time per frame beyond building it, so frame() plus any GPU wait.")
	for _, s := range scenes.All() {
		if only != "" && !slices.Contains(strings.Split(only, ","), s.Name()) {
			continue
		}
		var build, frame, piped Durations
		st = scenes.NewState()
		fps := 120.0
		if s.Name() == "hud" {
			fps = 4 // every HUD frame is a 4 Hz tick
		}
		draw := func(i int) (time.Duration, error) {
			st.Frame, st.T, st.Line = i, float64(i)/fps, i
			var b time.Duration
			t0 := time.Now()
			err := hl.Draw(func(f render.Frame) {
				record(c, s, st, f)
				b = time.Since(t0)
			})
			build = append(build, b)
			frame = append(frame, time.Since(t0)-b)
			return b, err
		}
		for i := range frames {
			if _, err := draw(i); err != nil {
				return err
			}
			if err := hl.Sync(); err != nil {
				return err
			}
		}
		build, frame = build[:frames:frames], frame[:frames:frames]
		const batch = 30
		for i := 0; i < frames; i += batch {
			t := time.Now()
			var cpu time.Duration
			for j := range batch {
				d, err := draw(frames + i + j)
				if err != nil {
					return err
				}
				cpu += d
			}
			if err := hl.Sync(); err != nil {
				return err
			}
			piped = append(piped, max(0, time.Since(t)-cpu)/batch)
		}
		fmt.Fprintf(out, "%-7s build:     %s\n        frame():   %s\n        pipelined: %s\n        RSS now:   %.0f MB\n", s.Name(), build, frame, piped, currentRSS())
	}
	names, missing := ts.Fallbacks()
	fmt.Fprintf(out, "system fallback fonts loaded: %q; %d runes had none\n", names, len(missing))
	fmt.Fprintf(out, "peak RSS after every scene: %.0f MB\n", peakRSS())
	memReport(out)
	return nil
}

// memReport splits memory into what Go holds and the rest (GPU driver, C libraries, mapped fonts).
func memReport(out io.Writer) {
	before := currentRSS()
	debug.FreeOSMemory()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	const mb = 1 << 20
	fmt.Fprintf(out, "RSS now %.0f MB, %.0f MB after returning free Go memory to the OS\n", before, currentRSS())
	fmt.Fprintf(out, "go heap in use %.0f MB, go runtime total %.0f MB (%.0f MB of it released); the rest of RSS is outside Go\n",
		float64(m.HeapInuse)/mb, float64(m.Sys)/mb, float64(m.HeapReleased)/mb)
}
