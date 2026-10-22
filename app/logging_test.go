package app_test

import (
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/liamg/oriel/app"
)

// Level: info by default; oriel --log-level=debug, ORIEL_LOG=debug, or :Log level debug live. And oriel --debug
// turns on debug logging and the frame-time overlay.
func TestConfigFromFlags(t *testing.T) {
	t.Setenv("ORIEL_LOG", "warn")
	for _, tt := range []struct {
		args  []string
		level slog.Level
		debug bool
	}{
		{nil, slog.LevelWarn, false},
		{[]string{"--log-level=error"}, slog.LevelError, false},
		{[]string{"--debug"}, slog.LevelDebug, true},
	} {
		cfg, err := app.ConfigFromFlags(tt.args, "v1", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Logs.Level.Level() != tt.level || cfg.Debug != tt.debug || cfg.CrashDir == "" || cfg.Version != "v1" {
			t.Errorf("%q: level %v debug %v crash dir %q", tt.args, cfg.Logs.Level.Level(), cfg.Debug, cfg.CrashDir)
		}
	}
	t.Setenv("ORIEL_LOG", "")
	if cfg, _ := app.ConfigFromFlags(nil, "v1", io.Discard); cfg.Logs.Level.Level() != slog.LevelInfo {
		t.Errorf("default level %v", cfg.Logs.Level.Level())
	}
	if cfg, _ := app.ConfigFromFlags([]string{"--debug", "a.go", "b/c.txt"}, "v1", io.Discard); len(cfg.Files) != 2 || cfg.Files[1] != "b/c.txt" {
		t.Errorf("files = %q", cfg.Files)
	}
	if _, err := app.ConfigFromFlags([]string{"--log-level=loud"}, "v1", io.Discard); err == nil {
		t.Error("took a level that doesn't exist")
	}
}

// Plain oriel opens nothing, oriel <dir> opens dir, and oriel <file>… opens files.
func TestConfigFromFlagsDirectory(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	for _, tt := range []struct {
		args  []string
		dir   string
		files int
	}{
		{nil, "", 0},
		{[]string{dir}, dir, 0},
		{[]string{"."}, wd, 0},
		{[]string{"a.go", "b.go"}, "", 2},
	} {
		cfg, err := app.ConfigFromFlags(tt.args, "v1", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.WorkDir != tt.dir || len(cfg.Files) != tt.files {
			t.Errorf("%q: dir %q files %q, want %q and %d files", tt.args, cfg.WorkDir, cfg.Files, tt.dir, tt.files)
		}
	}
}

// --cpuprofile names the file the CPU profile goes to.
func TestConfigFromFlagsCPUProfile(t *testing.T) {
	cfg, err := app.ConfigFromFlags([]string{"--cpuprofile", "/tmp/oriel.prof"}, "v1", io.Discard)
	if err != nil || cfg.CPUProfile != "/tmp/oriel.prof" {
		t.Fatalf("CPUProfile %q, err %v", cfg.CPUProfile, err)
	}
}
