//go:build spike

package main

import (
	"fmt"
	"image"
	"io"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"slices"
	"strings"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/op"

	"github.com/liamg/oriel/cmd/oriel-spike/scene"
)

var started = time.Now()

// benchmarks measures each scene headless: the CPU time to build a frame's ops (what
// shell.md's "< 4 ms CPU" budget is about) and the time for the GPU backend to draw it.
func benchmarks(out io.Writer, only string) error {
	const frames = 300
	fmt.Fprintf(out, "RSS at start: %.0f MB\n", currentRSS())
	sh, err := scene.NewFonts()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "RSS with fonts loaded: %.0f MB\n", currentRSS())
	win, err := headless.NewWindow(scene.IDEWidth, scene.IDEHeight)
	if err != nil {
		return err
	}
	defer win.Release()
	fmt.Fprintf(out, "RSS with the GPU context: %.0f MB\n", currentRSS())
	var ops op.Ops
	c := scene.NewCanvas(&ops, sh, 1)
	c.Mode, c.BakedShapes = textMode, bakedShapes

	st := scene.NewState()
	ops.Reset()
	c.Begin(&ops, 1)
	scene.IDE(c)
	if err := win.Frame(&ops); err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	_ = win.Screenshot(img)
	fmt.Fprintf(out, "startup: first ide frame drawn %s after start (headless)\n", time.Since(started).Round(time.Millisecond))
	fmt.Fprintf(out, "RSS after the first ide frame: %.0f MB now, %.0f MB peak\n", currentRSS(), peakRSS())

	fmt.Fprintf(out, "%s %s/%s, %d frames per scene at 1600×1000 @1×, text=%s shapes=%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, frames, textMode, map[bool]string{false: "paths", true: "baked"}[bakedShapes])
	fmt.Fprintln(out, "frame(): Gio's CPU work to collect, encode and submit. sync: a 1-pixel readback, which waits for the GPU")
	fmt.Fprintln(out, "  and includes its wake-up after idling. pipelined: frames submitted back to back, one sync per batch of 30,")
	fmt.Fprintln(out, "  so it's GPU time per frame under load.")
	for _, s := range scene.All() {
		if only != "" && !slices.Contains(strings.Split(only, ","), s.Name()) {
			continue
		}
		var build, frame, sync, piped scene.Durations
		st = scene.NewState()
		draw := func(i int) (time.Duration, error) {
			st.Frame, st.T, st.Line = i, float64(i)/120, i
			t0 := time.Now()
			ops.Reset()
			c.Begin(&ops, 1)
			s.Draw(c, st)
			t1 := time.Now()
			build = append(build, t1.Sub(t0))
			err := win.Frame(&ops)
			frame = append(frame, time.Since(t1))
			return t1.Sub(t0), err
		}
		for i := range frames {
			if _, err := draw(i); err != nil {
				return err
			}
			t := time.Now()
			_ = win.Screenshot(img)
			sync = append(sync, time.Since(t))
		}
		// build and frame() are reported from the frames with a sync each.
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
			_ = win.Screenshot(img)
			piped = append(piped, (time.Since(t)-cpu)/batch)
		}
		fmt.Fprintf(out, "%-7s build ops: %s\n        frame():   %s\n        sync:      %s\n        pipelined: %s\n        RSS now:   %.0f MB\n", s.Name(), build[:frames], frame[:frames], sync, piped, currentRSS())
	}
	files, missing := sh.Fallbacks()
	fmt.Fprintf(out, "system fallback fonts loaded: %q; %d runes had none\n", files, len(missing))
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
	if memProfile == "" {
		return
	}
	f, err := os.Create(memProfile)
	if err != nil {
		fmt.Fprintln(out, "memprofile:", err)
		return
	}
	defer f.Close()
	pprof.WriteHeapProfile(f)
}
