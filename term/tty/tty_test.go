//go:build linux || darwin

package tty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/term/pty"
)

// The Finder, the Dock and launchd give an app /dev/null for stdin: a character device, but not a terminal.
func TestNullIsNotATerminal(t *testing.T) {
	isolate.Test(t)
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if IsTerminal(f) || Foreground(f) {
		t.Error("/dev/null was taken for a terminal")
	}
}

// On a terminal, the modes set are the modes read back, and a process the terminal started is in its foreground.
func TestModesOnATerminal(t *testing.T) {
	isolate.Test(t)
	if os.Getenv("ORIEL_TTY_REPORT") == "1" {
		fmt.Println(report())
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestModesOnATerminal$")
	cmd.Env = append(os.Environ(), "ORIEL_TTY_REPORT=1")
	p, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	out, _ := io.ReadAll(p)
	_ = cmd.Wait()
	if !strings.Contains(string(out), "terminal=true foreground=true raw=true restored=true") {
		t.Errorf("on a pty: %q", out)
	}
}

func report() string {
	in := os.Stdin
	before, err := Get(in)
	if err != nil {
		return err.Error()
	}
	raw := *before
	raw.Lflag &^= unix.ISIG | unix.ICANON | unix.ECHO
	if err := Set(in, &raw); err != nil {
		return err.Error()
	}
	got, _ := Get(in)
	isRaw := got.Lflag&(unix.ISIG|unix.ICANON|unix.ECHO) == 0
	if err := Set(in, before); err != nil {
		return err.Error()
	}
	got, _ = Get(in)
	return fmt.Sprintf("terminal=%v foreground=%v raw=%v restored=%v", IsTerminal(in), Foreground(in), isRaw,
		got.Lflag == before.Lflag)
}
