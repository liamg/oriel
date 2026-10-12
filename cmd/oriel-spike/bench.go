//go:build spike

package main

import (
	"fmt"
	"image"
	"io"
	"runtime"
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
	sh, err := scene.NewShaper(false)
	if err != nil {
		return err
	}
	win, err := headless.NewWindow(scene.IDEWidth, scene.IDEHeight)
	if err != nil {
		return err
	}
	defer win.Release()
	var ops op.Ops
	c := scene.NewCanvas(&ops, sh, 1)

	st := scene.NewState()
	ops.Reset()
	scene.IDE(c)
	if err := win.Frame(&ops); err != nil {
		return err
	}
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	_ = win.Screenshot(img)
	fmt.Fprintf(out, "startup: first ide frame drawn %s after start (headless)\n", time.Since(started).Round(time.Millisecond))
	fmt.Fprintf(out, "RSS after the first ide frame: %.0f MB (peak so far)\n", peakRSS())

	fmt.Fprintf(out, "%s %s/%s, %d frames per scene at 1600×1000 @1×\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, frames)
	for _, s := range scene.All() {
		var build, draw scene.Durations
		st = scene.NewState()
		for i := range frames {
			st.Frame, st.T, st.Line = i, float64(i)/120, i
			t0 := time.Now()
			ops.Reset()
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
	return nil
}
