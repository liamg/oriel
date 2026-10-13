package app

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core/logs"
)

// Logs is where records go: the ring the log panel reads, Oriel's file and the session logs (errors.md).
type Logs struct {
	Ring   *logs.Ring
	Router *logs.Router
	Level  *slog.LevelVar
	Redact *logs.Redactor
	file   *logs.File
}

type LogOpts struct {
	File        string // Oriel's log file; none if ""
	SessionLogs logs.SessionLogs
	Level       slog.Level
	RingSize    int // 5000 if 0
}

func NewLogs(o LogOpts) *Logs {
	if o.RingSize == 0 {
		o.RingSize = 5000
	}
	l := &Logs{Ring: logs.NewRing(o.RingSize), Level: new(slog.LevelVar), Redact: logs.NewRedactor()}
	l.Level.Set(o.Level)
	l.Router = &logs.Router{Ring: l.Ring, Sessions: o.SessionLogs}
	if o.File != "" {
		l.file = &logs.File{Path: o.File}
		l.Router.Oriel = l.file
	}
	return l
}

// Logger logs through the redactor into the router, as source.
func (l *Logs) Logger(source string) *slog.Logger {
	return slog.New(logs.NewHandler(l.Router, l.Level, l.Redact)).With(logs.KeySource, source)
}

func (l *Logs) Close() error {
	err := l.Router.Close()
	if l.file != nil {
		if e := l.file.Close(); err == nil {
			err = e
		}
	}
	return err
}

// ParseLevel reads a level name: debug, info, warn or error.
func ParseLevel(s string) (slog.Level, bool) {
	var lv slog.Level
	if err := lv.UnmarshalText([]byte(strings.ToUpper(strings.TrimSpace(s)))); err != nil {
		return 0, false
	}
	return lv, true
}

// LevelFromEnv is ORIEL_LOG's level, or def.
func LevelFromEnv(def slog.Level) slog.Level {
	if lv, ok := ParseLevel(os.Getenv("ORIEL_LOG")); ok {
		return lv
	}
	return def
}

// ConfigFromFlags is the app's config from its command line: --log-level (or ORIEL_LOG), --debug, and files to
// open, with the log, session logs and crash reports in their places on this platform.
func ConfigFromFlags(args []string, version string, stderr io.Writer) (Config, error) {
	fs := flag.NewFlagSet("oriel", flag.ContinueOnError)
	fs.SetOutput(stderr)
	level := fs.String("log-level", "", "debug, info, warn or error (default info, or $ORIEL_LOG)")
	debug := fs.Bool("debug", false, "debug logging and the frame-time overlay")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	lv := LevelFromEnv(slog.LevelInfo)
	if *level != "" {
		l, ok := ParseLevel(*level)
		if !ok {
			return Config{}, fmt.Errorf("no log level %q: debug, info, warn or error", *level)
		}
		lv = l
	}
	if *debug {
		lv = slog.LevelDebug
	}
	state := logs.StateDir()
	return Config{
		Logs: NewLogs(LogOpts{File: filepath.Join(logs.Dir(), "oriel.log"), SessionLogs: logs.Dirs{Root: filepath.Join(state, "sessions")},
			Level: lv}),
		CrashDir: filepath.Join(state, "crashes"),
		Version:  version,
		Debug:    *debug,
		Files:    fs.Args(),
	}, nil
}
