package app

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core/logs"
)

// ConfigFromFlags is the app's config from its command line: --log-level (or ORIEL_LOG), --debug, and files to
// open, with the log, session logs and crash reports in their places on this platform.
func ConfigFromFlags(args []string, version string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("oriel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	level := fs.String("log-level", "", "debug, info, warn or error (default info, or $ORIEL_LOG)")
	debug := fs.Bool("debug", false, "debug logging and the frame-time overlay")
	cpuprofile := fs.String("cpuprofile", "", "write a CPU profile to this file while the window is open")
	tracefile := fs.String("trace", "", "write the execution trace of the seconds before a stall to this file, for go tool trace")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	lv := shell.LevelFromEnv(slog.LevelInfo)
	if *level != "" {
		l, ok := shell.ParseLevel(*level)
		if !ok {
			return Config{}, fmt.Errorf("no log level %q: debug, info, warn or error", *level)
		}
		lv = l
	}
	if *debug {
		lv = slog.LevelDebug
	}
	state := logs.StateDir()
	files, dir := fs.Args(), ""
	if len(files) == 1 {
		if st, err := os.Stat(files[0]); err == nil && st.IsDir() {
			dir, _ = filepath.Abs(files[0])
			files = nil
		}
	}
	return Config{
		Logs: NewLogs(LogOpts{File: filepath.Join(logs.Dir(), "oriel.log"), SessionLogs: logs.Dirs{Root: filepath.Join(state, "sessions")},
			Level: lv}),
		CrashDir:   filepath.Join(state, "crashes"),
		Version:    version,
		Debug:      *debug,
		CPUProfile: *cpuprofile,
		Trace:      *tracefile,
		Files:      files,
		WorkDir:    dir,
	}, nil
}
