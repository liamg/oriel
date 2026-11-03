package shell

import (
	"log/slog"
	"os"
	"strings"
	"sync/atomic"

	"github.com/liamg/oriel/core/logs"
)

// Logs is where records go: the ring the log panel reads, Oriel's file and the session logs.
type Logs struct {
	Ring   *logs.Ring
	Router *logs.Router
	Level  *slog.LevelVar
	Redact *logs.Redactor
	file   *logs.File
	writes atomic.Uint64 // the records the ring has taken
	shown  atomic.Bool   // a view shows the ring's records: a write asks for a frame
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
	l.Ring.OnWrite(func() { l.writes.Add(1) })
	l.Router = &logs.Router{Ring: l.Ring, Sessions: o.SessionLogs}
	if o.File != "" {
		l.file = &logs.File{Path: o.File}
		l.Router.Oriel = l.file
	}
	return l
}

// OnShownWrite calls fn after each write while a view shows the ring's records (Showing).
func (l *Logs) OnShownWrite(fn func()) {
	l.Ring.OnWrite(func() {
		if l.shown.Load() {
			fn()
		}
	})
}

// Showing is held while a view shows the ring's records; release lets go.
func (l *Logs) Showing() (release func()) {
	l.shown.Store(true)
	return func() { l.shown.Store(false) }
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
