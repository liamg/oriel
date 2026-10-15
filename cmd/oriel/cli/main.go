// Package cli is the oriel command: the desktop app, the worker and the CLI. cmd/oriel's main runs Main, and so
// does a custom build's (`oriel build --with`).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/cmd/oriel/dev"
	"github.com/liamg/oriel/integ/slack"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/render/gio"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/worker"
)

var version = "dev"

// Main runs oriel with the process's arguments, and exits.
func Main() {
	ossandbox.Main()
	args := os.Args[1:]
	if handled, code := runSandboxed(args); handled {
		os.Exit(code)
	}
	if len(args) > 0 && args[0] == "app" {
		args = args[1:]
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") || !subcommands[args[0]] {
		runApp(args)
		return
	}
	os.Exit(run(args, os.Stdout, os.Stderr))
}

// runApp opens the app's window. Gio's event loop needs the main goroutine, so the app runs on another.
func runApp(args []string) {
	cfg, err := app.ConfigFromFlags(args, version, os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	cfg.Plugins = append(cfg.Plugins, app.ScriptPlugin(slack.Files))
	cfg.Plugins = append(cfg.Plugins, plugin.Native()...)
	appUpdates(&cfg)
	cfg.LocalWorker = func(ctx context.Context) (worker.Client, error) { return worker.DialLocal(ctx, orielHome(), "") }
	if c, err := localWorker(); err != nil {
		fmt.Fprintln(os.Stderr, "oriel: no worker, so no sessions:", err)
	} else {
		cfg.Workers = append(cfg.Workers, c)
		cfg.Logs.Router.Sessions = c.Logs() // session logs live on the worker (AI-1)
	}
	be, err := gio.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
	go func() {
		defer cfg.Logs.Close()
		if err := app.Run(context.Background(), be, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "oriel:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	gio.Main()
}

// subcommands are the first arguments that aren't files to open.
var subcommands = map[string]bool{"version": true, "worker": true, "dev": true, "hook": true, "new": true, "review": true,
	"init": true, "repos": true, "sessions": true, "build": true, "update": true}

func run(args []string, stdout, stderr io.Writer) int {
	switch args[0] {
	case "version":
		fmt.Fprintln(stdout, "oriel", version)
		for _, e := range plugin.Native() {
			fmt.Fprintln(stdout, "with", e.Manifest().ID)
		}
		return 0
	case "build":
		if err := runBuild(args[1:], stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "oriel build:", err)
			return 1
		}
		return 0
	case "worker":
		if err := runWorker(args[1:], stderr); err != nil {
			fmt.Fprintln(stderr, "oriel worker:", err)
			return 1
		}
		return 0
	case "update":
		if err := runUpdate(args[1:], stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "oriel update:", err)
			return 1
		}
		return 0
	case "hook":
		return runHook(args[1:], os.Stdin, stdout, stderr)
	case "new", "review", "init", "repos", "sessions":
		if err := runSessionCLI(args[0], args[1:], stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "oriel %s: %v\n", args[0], err)
			return 1
		}
		return 0
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

const devUsage = `oriel dev shot [-scales 1,2] [-light] DIR
                                       render every reference scene offscreen into DIR
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
	light := fs.Bool("light", false, "the built-in theme's light variant")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	rest := fs.Args()
	switch args[0] {
	case "shot":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[0])
		}
		v := theme.Dark
		if *light {
			v = theme.Light
		}
		return dev.Shots(rest[0], *scales, v)
	case "compare":
		if len(rest) != 3 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[2])
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
