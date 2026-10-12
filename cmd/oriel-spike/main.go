//go:build spike

// Command oriel-spike is the phase-0 Gio spike: the 01-ide mockup drawn natively, plus the
// scenes and measurements in _design/shell.md. See _design/spikes/P0-4-gio.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/pprof"
	"strings"

	"github.com/liamg/oriel/cmd/oriel-spike/scene"
)

// stopProfile ends -cpuprofile. The window exits with os.Exit, so it calls this itself.
func stopProfile() { pprof.StopCPUProfile() }

var (
	textMode    scene.TextMode
	bakedShapes bool
	memProfile  string
)

func main() {
	shot := flag.String("shot", "", "render every scene headless into this directory as PNGs, then exit")
	scales := flag.String("scales", "1,2", "device scales for -shot")
	bench := flag.Bool("bench", false, "run the headless benchmarks and print the numbers")
	auto := flag.Bool("auto", false, "windowed: run the scripted measurements, print them, exit")
	cmp := flag.String("compare", "", "mockup.png,native.png,out.png: write them side by side with a diff")
	sceneName := flag.String("scene", "ide", "windowed: the scene to open (ide, scroll, text, glow, hud, type)")
	cpuprofile := flag.String("cpuprofile", "", "write a CPU profile here")
	memprofile := flag.String("memprofile", "", "write a heap profile here on exit (-bench)")
	textFlag := flag.String("text", "paths", "text path: paths (Gio vector glyphs), atlas (glyph atlas quads) or runs (one cached image per run)")
	phases := flag.String("phases", "", "run only these scenes, comma-separated (e.g. hud): -auto's phases, or -bench's scenes")
	shapesFlag := flag.String("shapes", "paths", "rounded rects and borders: paths, or baked (9-slices of baked corners)")
	flag.Parse()
	mode, err := scene.ParseTextMode(*textFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel-spike:", err)
		os.Exit(2)
	}
	textMode, memProfile, bakedShapes = mode, *memprofile, *shapesFlag == "baked"
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer stopProfile()
		}
	}

	switch {
	case *shot != "":
		err = shots(*shot, *scales)
	case *cmp != "":
		p := strings.Split(*cmp, ",")
		if len(p) != 3 {
			err = fmt.Errorf("-compare wants three comma-separated paths")
			break
		}
		err = compare(p[0], p[1], p[2])
	case *bench:
		err = benchmarks(os.Stdout, *phases)
	default:
		err = runWindow(*sceneName, *auto, *phases)
	}
	if err != nil {
		stopProfile()
		fmt.Fprintln(os.Stderr, "oriel-spike:", err)
		os.Exit(1)
	}
}
