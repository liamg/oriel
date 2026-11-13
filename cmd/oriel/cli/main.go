// Package cli is the oriel command: the desktop app and the CLI. cmd/oriel's main runs Main, and so
// does a custom build's (`oriel build --with`).
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/cmd/oriel/dev"
	"github.com/liamg/oriel/integ"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/ossandbox"
	"github.com/liamg/oriel/render/gio"
	"github.com/liamg/oriel/sandbox/helper"
	"github.com/liamg/oriel/sysload"
	"github.com/liamg/oriel/term/shellenv"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/update"
)

var version = "dev"

// Main runs oriel with the process's arguments, and exits.
func Main() {
	ossandbox.Main()
	args := os.Args[1:]
	if handled, code := runSandboxed(args); handled {
		os.Exit(code)
	}
	// Docker sandboxes run the helper this build carries; a build without one is unfinished.
	if !helper.Embedded() {
		fmt.Fprintln(os.Stderr, "oriel:", helper.ErrMissing)
		os.Exit(1)
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

// runApp opens the app's window. Gio's event loop needs the main goroutine, so the app runs on another. Started from
// a terminal, the app runs detached from it (detach).
func runApp(args []string) {
	fromTerminal := detach()
	cfg, err := app.ConfigFromFlags(args, version, os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	stopProfile := cpuProfile(cfg.CPUProfile)
	cfg.Quit = quitOnSignal(stopProfile)
	cfg.Bundled = integ.Bundled()
	cfg.Plugins = append(cfg.Plugins, plugin.Native()...)
	home := orielHome()
	in, _, _ := selfInstaller()
	if err := claim(home, &cfg, in); err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
	log := cfg.Logs.Logger("host")
	loginPath(log, fromTerminal)
	// load reads the machine's load in-process, only while it's wanted, and says when it moves.
	load := sysload.NewSampler(5*time.Second, nil)
	sandboxes := newSandboxes()
	cfg.SandboxBackends = sandboxes.Offered
	w, err := newHost(home, log, load, sandboxes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel: no sessions:", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithCancel(context.Background())
	startHost(ctx, w, log, load)
	cfg.Host, cfg.Load = w, load
	cfg.Agents = builtinAgents(home)
	cfg.Logs.Router.Sessions = w.Logs()
	be, err := gio.New()
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
	watch := &exitWatch{clock: clock.Real(), after: exitAfter, log: log, stderr: os.Stderr, exit: func(code int) {
		_ = cfg.Logs.Close()
		os.Exit(code)
	}}
	cfg.Closed = watch.start
	go func() {
		err := app.Run(context.Background(), be, cfg)
		os.Exit(shutdown(err, cfg.Logs, cancel, func() { w.Close() }, stopProfile))
	}()
	gio.Main()
}

// loginPath gives the process the PATH and ssh agent the user's login shell sets up, when it wasn't started from a
// terminal: opened from the Finder, the Dock or a launcher, it has the desktop's, so the agents' programs (claude,
// codex) aren't on its PATH, and a key in the agent the shell names (1Password's) isn't reached.
func loginPath(log *slog.Logger, fromTerminal bool) {
	if fromTerminal {
		return
	}
	if v, err := shellenv.Apply(context.Background()); err != nil {
		log.Warn("the login shell's PATH couldn't be read; keeping the desktop's", "shell", shellenv.Shell(), "err", err)
	} else {
		log.Info("PATH and ssh agent from the login shell", "shell", shellenv.Shell(), "path", v.Path, "ssh_auth_sock", v.SSHAuthSock)
	}
}

// claim takes home for this process, then confirms or rolls back a pending update: only the Oriel holding the
// lock may, or a second launch would count as the new version's start.
func claim(home string, cfg *app.Config, in *update.Installer) error {
	var err error
	if held, err = lock(home); err != nil {
		return err
	}
	if err := oldWorker(home); err != nil {
		return err
	}
	if in != nil {
		appUpdates(cfg, in)
	}
	return nil
}

// shutdown stops what's running once the window has closed, then the log, and is the exit code.
func shutdown(err error, log io.Closer, stop ...func()) int {
	for _, f := range stop {
		f()
	}
	code := 0
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		code = 1
	}
	_ = log.Close()
	return code
}

// cpuProfile starts a CPU profile into path, if there is one, and returns what stops it; ^C stops it and exits.
func cpuProfile(path string) func() {
	if path == "" {
		return func() {}
	}
	f, err := os.Create(path)
	if err == nil {
		err = pprof.StartCPUProfile(f)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel: cpu profile:", err)
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			pprof.StopCPUProfile()
			f.Close()
		})
	}
}

// quitOnSignal asks the app to quit on SIGINT or SIGTERM; a second doesn't wait for the turns, and a third exits,
// after stop.
func quitOnSignal(stop func()) <-chan struct{} {
	quit := make(chan struct{}, 1)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		for n := 1; ; n++ {
			<-sig
			if n == 3 {
				stop()
				os.Exit(130)
			}
			select {
			case quit <- struct{}{}:
			default:
			}
		}
	}()
	return quit
}

// subcommands are the first arguments that aren't files to open.
var subcommands = map[string]bool{"version": true, "dev": true, "hook": true, "build": true, "update": true, "plugin": true,
	"plugin-process": true}

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
	case "plugin":
		if err := runPlugin(args[1:], stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "oriel plugin:", err)
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
	case "plugin-process":
		if err := runPluginProcess(args[1:], os.Stdin, stdout); err != nil {
			fmt.Fprintln(stderr, "oriel plugin-process:", err)
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
oriel dev icon [-light] DIR            render the app icon into DIR as a macOS iconset (for iconutil)
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
	v := theme.Dark
	if *light {
		v = theme.Light
	}
	switch args[0] {
	case "shot":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[0])
		}
		return dev.Shots(rest[0], *scales, v)
	case "icon":
		if len(rest) != 1 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[2])
		}
		return dev.Icons(rest[0], v)
	case "compare":
		if len(rest) != 3 {
			return fmt.Errorf("usage: %s", strings.Split(devUsage, "\n")[3])
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
