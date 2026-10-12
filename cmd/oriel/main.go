// Command oriel is the desktop app, the worker and the CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/cmd/oriel/dev"
	"github.com/liamg/oriel/render/gio"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 || os.Args[1] == "app" {
		runApp()
		return
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// runApp opens the app's window. Gio's event loop needs the main goroutine, so the app runs on another.
func runApp() {
	be, err := gio.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
	go func() {
		if err := app.Run(context.Background(), be, app.Config{}); err != nil {
			fmt.Fprintln(os.Stderr, "oriel:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	gio.Main()
}

func run(args []string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "oriel", version)
		return 0
	case "worker":
		fmt.Fprintf(stderr, "oriel %s: not implemented yet\n", args[0])
		return 1
	case "dev":
		if err := runDev(args[1:], stdout); err != nil {
			fmt.Fprintln(stderr, "oriel dev:", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "oriel: unknown command %q\n", args[0])
		return 2
	}
}

const devUsage = `oriel dev shot [-scales 1,2] DIR      render every reference scene offscreen into DIR
oriel dev compare MOCKUP NATIVE OUT    write them side by side with a diff, and print how far apart they are
oriel dev bench [-scenes a,b]          measure every scene offscreen: build, frame(), GPU time, RSS
oriel dev window [-auto] [-scenes a,b] [SCENE]
                                       open a scene in a window; -auto runs the budget measurements and exits`

func runDev(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage:\n%s", devUsage)
	}
	fs := flag.NewFlagSet("oriel dev "+args[0], flag.ContinueOnError)
	scales := fs.String("scales", "1,2", "device scales")
	only := fs.String("scenes", "", "only these scenes, comma-separated")
	auto := fs.Bool("auto", false, "run the scripted measurements, print them, exit")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	rest := fs.Args()
	switch args[0] {
	case "shot":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[0])
		}
		return dev.Shots(rest[0], *scales)
	case "compare":
		if len(rest) != 3 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[1])
		}
		return dev.Compare(stdout, rest[0], rest[1], rest[2])
	case "bench":
		return dev.Bench(stdout, *only)
	case "window":
		scene := "ide"
		if len(rest) > 0 {
			scene = rest[0]
		}
		return dev.Window(scene, *auto, *only)
	}
	return fmt.Errorf("unknown command %q\nusage:\n%s", args[0], devUsage)
}
