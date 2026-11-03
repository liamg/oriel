package cli

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Only one Oriel at a time: a second Oriel on the same home is refused while the first holds the lock,
// and the OS lets it go when the first ends.
func TestOnlyOneOrielAtATime(t *testing.T) {
	if os.Getenv("ORIEL_LOCK_HOME") != "" {
		if _, err := lock(os.Getenv("ORIEL_LOCK_HOME")); errors.Is(err, ErrRunning) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	home := t.TempDir()
	f, err := lock(home)
	if err != nil {
		t.Fatal(err)
	}
	second := func() int {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOnlyOneOrielAtATime$")
		cmd.Env = append(os.Environ(), "ORIEL_LOCK_HOME="+home)
		err := cmd.Run()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := second(); code != 3 {
		t.Errorf("a second process on the same home exited %d, want refused (3)", code)
	}
	f.Close()
	if code := second(); code != 0 {
		t.Errorf("after the first let go, a second exited %d", code)
	}
}

// An `oriel worker` from an earlier release still answering on worker.sock stops the app; a
// socket nothing answers on is deleted.
func TestAnOldWorkerStopsTheApp(t *testing.T) {
	home, err := os.MkdirTemp("", "oh") // a unix socket's path is short
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(home)
	path := filepath.Join(home, "worker.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldWorker(home); err == nil || !strings.Contains(err.Error(), "pkill -f 'oriel worker'") {
		t.Errorf("with a host listening: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stale socket should still be there for the test: %v", err)
	}
	if err := oldWorker(home); err != nil {
		t.Errorf("with a stale socket: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the stale socket is still there: %v", err)
	}
	if err := oldWorker(home); err != nil {
		t.Errorf("with no socket: %v", err)
	}
}
