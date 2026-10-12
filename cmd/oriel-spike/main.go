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
)

func main() {
	shot := flag.String("shot", "", "render every scene headless into this directory as PNGs, then exit")
	scales := flag.String("scales", "1,2", "device scales for -shot")
	bench := flag.Bool("bench", false, "run the headless benchmarks and print the numbers")
	auto := flag.Bool("auto", false, "windowed: run the scripted measurements, print them, exit")
	cmp := flag.String("compare", "", "mockup.png,native.png,out.png: write them side by side with a diff")
	sceneName := flag.String("scene", "ide", "windowed: the scene to open (ide, scroll, text, glow, hud, type)")
	cpuprofile := flag.String("cpuprofile", "", "write a CPU profile here")
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err == nil {
			pprof.StartCPUProfile(f)
			defer pprof.StopCPUProfile()
		}
	}

	var err error
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
		err = benchmarks(os.Stdout)
	default:
		err = runWindow(*sceneName, *auto)
	}
	if err != nil {
		pprof.StopCPUProfile()
		fmt.Fprintln(os.Stderr, "oriel-spike:", err)
		os.Exit(1)
	}
}
