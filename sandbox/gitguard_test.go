package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config holding only data passes; one that would run a command doesn't. A Guard remembers a file's verdict, but
// only until the file changes: an agent can't get a safe verdict kept for a config it has since made unsafe.
func TestGuardVetsConfigUntilItChanges(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config")
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(cfg, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var g Guard
	write("[user]\n\tname = Ada\n")
	if err := g.CheckConfig(cfg); err != nil {
		t.Fatalf("a safe config: %v", err)
	}
	write("[user]\n\tname = Ada\n[core]\n\tsshCommand = touch /tmp/pwned\n")
	err := g.CheckConfig(cfg)
	if err == nil || !strings.Contains(err.Error(), "core.sshcommand") {
		t.Fatalf("the config changed to run a command: %v", err)
	}
	if err := CheckConfig(cfg); err == nil {
		t.Error("CheckConfig without a Guard passed an unsafe config")
	}
	if err := g.CheckConfig(filepath.Join(t.TempDir(), "missing")); err != nil {
		t.Errorf("a missing config: %v", err)
	}
}
