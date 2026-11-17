//go:build perf

package app_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/kit"
)

// The idle budgets leave the app alone for a while on the real clock, once it's idle: they see what
// TestIdlePollsNoFasterThanASecond can't, a timer of the system's that starts a process or draws a frame.

// The idle budget: three quiet sessions, one agent working and the window behind another app
// spawn no process and run no poll faster than a second over 10s.
func TestIdleBudget(t *testing.T) {
	spawned := spawns(t)
	r := idleWindow(t)
	r.Idle() // what opening the sessions started (reading each one's git) is done
	w, ok := kit.WindowOf(r.Host)
	if !ok {
		t.Fatal("no window service")
	}
	polls := w.Polls()
	polls.Reset()
	before := len(spawned())
	time.Sleep(10 * time.Second)
	if ran := spawned()[before:]; len(ran) > 0 {
		t.Errorf("spawned %d processes while idle: %v", len(ran), ran)
	}
	if d := polls.FastestSince(); d > 0 && d < time.Second {
		t.Errorf("a poll ran every %v while idle, want a second or slower", d)
	}
}

// The idle budget's frames: at most 2 over 10s with the window behind another app.
func TestIdleFramesBudget(t *testing.T) {
	r := idleWindow(t)
	r.Idle()
	before := len(r.Win.Frames())
	time.Sleep(10 * time.Second)
	if n := len(r.Win.Frames()) - before; n > 2 {
		t.Errorf("drew %d frames while idle, want at most 2", n)
	}
}

// spawns puts shims for the commands Oriel runs on PATH, each noting its run in a log, and counts the runs logged.
func spawns(t *testing.T) func() []string {
	t.Helper()
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "spawned")
	for _, name := range []string{"git", "gh", "rg", "claude", "codex", "docker", "ps", "sysctl", "vm_stat", "defaults", "gdbus", "gsettings"} {
		real, err := exec.LookPath(name)
		run := "exit 127"
		if err == nil {
			run = `exec "` + real + `" "$@"`
		}
		shim := "#!/bin/sh\necho \"" + name + " $*\" | tr ' ' , >> \"" + log + "\"\n" + run + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(shim), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		b, _ := os.ReadFile(log)
		return strings.Fields(string(b))
	}
}
