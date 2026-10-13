package app

import (
	"io"
	"log/slog"
	"testing"
)

// errors.md: "Level: info by default; oriel --log-level=debug, ORIEL_LOG=debug, or :Log level debug live." And
// "oriel --debug turns on debug logging and the frame-time overlay."
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
		cfg, err := ConfigFromFlags(tt.args, "v1", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.Logs.Level.Level() != tt.level || cfg.Debug != tt.debug || cfg.CrashDir == "" || cfg.Version != "v1" {
			t.Errorf("%q: level %v debug %v crash dir %q", tt.args, cfg.Logs.Level.Level(), cfg.Debug, cfg.CrashDir)
		}
	}
	t.Setenv("ORIEL_LOG", "")
	if cfg, _ := ConfigFromFlags(nil, "v1", io.Discard); cfg.Logs.Level.Level() != slog.LevelInfo {
		t.Errorf("default level %v", cfg.Logs.Level.Level())
	}
	if _, err := ConfigFromFlags([]string{"--log-level=loud"}, "v1", io.Discard); err == nil {
		t.Error("took a level that doesn't exist")
	}
}
