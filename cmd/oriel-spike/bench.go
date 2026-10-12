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
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/op"

	"github.com/liamg/oriel/cmd/oriel-spike/scene"
)

var started = time.Now()

// benchmarks measures each scene headless: the CPU time to build a frame's ops (what
// shell.md's "< 4 ms CPU" budget is about) and the time for the GPU backend to draw it.
func benchmarks(out io.Writer) error {
	const frames = 300
	fmt.Fprintf(out, "RSS at start: %.0f MB\n", currentRSS())
	sh, err := scene.NewShaper(false)
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
	for _, s := range scene.All() {
		var build, draw scene.Durations
		st = scene.NewState()
		for i := range frames {
			st.Frame, st.T, st.Line = i, float64(i)/120, i
			t0 := time.Now()
			ops.Reset()
			c.Begin(&ops, 1)
			s.Draw(c, st)
			t1 := time.Now()
			if err := win.Frame(&ops); err != nil {
				return err
			}
			// Screenshot waits for the GPU to finish the frame.
			_ = win.Screenshot(img)
			build = append(build, t1.Sub(t0))
			draw = append(draw, time.Since(t1))
		}
		fmt.Fprintf(out, "%-7s build ops: %s\n        gpu draw:  %s\n", s.Name(), build, draw)
	}
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
