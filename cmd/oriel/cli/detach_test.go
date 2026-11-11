package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/term/pty"
	"github.com/liamg/oriel/term/tty"
)

// Started from a terminal, the app runs with none: it and everything it starts are on a session of their own, can't
// open /dev/tty, and read /dev/null, so nothing it runs (a passphrase prompt, pinentry) can leave the terminal raw.
// What it prints still reaches the terminal.
func TestFromATerminalTheAppHasNoTerminal(t *testing.T) {
	out := detachTest(t, "notty")
	cmd := exec.Command(os.Args[0], "-test.run=^TestFromATerminalTheAppHasNoTerminal$")
	p, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer p.Close()
	printed, _ := io.ReadAll(p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("supervisor: %v; printed %q", err, printed)
	}
	if !strings.Contains(string(printed), "the app says hello") {
		t.Errorf("the app's output didn't reach the terminal: %q", printed)
	}
	r := readReport(t, out+".app")
	if r["fromTerminal"] != true {
		t.Error("the app doesn't know it was started from a terminal")
	}
	if r["ttyOpens"] != false {
		t.Error("the app could open /dev/tty")
	}
	if r["stdinTerminal"] != false {
		t.Error("the app's stdin is a terminal")
	}
	if r["ownSession"] != true {
		t.Error("the app isn't on a session of its own")
	}
	if r["ppid"] != float64(cmd.Process.Pid) {
		t.Errorf("the app's parent is %v, want the supervisor %d", r["ppid"], cmd.Process.Pid)
	}
	if r["marker"] != "" {
		t.Errorf("the app's environment still marks it: %q, so what it starts would take itself for the app", r["marker"])
	}
}

// Not from a terminal (the Finder, the Dock, a launcher), the app runs in the process it was started in.
func TestNotFromATerminalTheAppRunsInProcess(t *testing.T) {
	out := detachTest(t, "notty")
	cmd := exec.Command(os.Args[0], "-test.run=^TestNotFromATerminalTheAppRunsInProcess$")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	r := readReport(t, out+".app")
	if r["fromTerminal"] != false {
		t.Error("the app took itself for started from a terminal")
	}
	if r["ppid"] != float64(os.Getpid()) {
		t.Errorf("the app's parent is %v, want the test %d: something stood between", r["ppid"], os.Getpid())
	}
}

// When the app ends, the terminal has the modes it had when Oriel started, whatever a program changed them to.
func TestTheTerminalsModesAreRestored(t *testing.T) {
	out := detachTest(t, "restore")
	cmd := exec.Command(os.Args[0], "-test.run=^TestTheTerminalsModesAreRestored$")
	p, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer p.Close()
	printed, _ := io.ReadAll(p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("supervisor: %v; printed %q", err, printed)
	}
	if r := readReport(t, out+".app"); r["raw"] != true {
		t.Fatalf("the app couldn't set the terminal raw for the test: %v", r)
	}
	r := readReport(t, out+".supervisor")
	if r["code"] != float64(0) || r["restored"] != true {
		t.Errorf("after the app: %v", r)
	}
}

// ^C on the terminal reaches the app once per press, as before, and the app's exit is the process's: a signal's
// death is 128+n.
func TestSignalsReachTheApp(t *testing.T) {
	out := detachTest(t, "signals")
	cmd := exec.Command(os.Args[0], "-test.run=^TestSignalsReachTheApp$")
	p, err := pty.Start(cmd, 80, 24)
	if err != nil {
		t.Skipf("no pseudo-terminal here: %v", err)
	}
	defer p.Close()
	var printed bytes.Buffer
	readUntil(t, p, &printed, "ready")
	for n := 1; n <= 2; n++ {
		if _, err := p.Write([]byte{3}); err != nil { // ^C
			t.Fatal(err)
		}
		readUntil(t, p, &printed, fmt.Sprintf("interrupt %d\r\n", n))
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(p)
	printed.Write(rest)
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 128+int(syscall.SIGTERM) {
		t.Errorf("supervisor ended with %v, want exit %d; printed %q", err, 128+int(syscall.SIGTERM), printed.String())
	}
	if r := readReport(t, out+".app"); r["interrupts"] != float64(2) {
		t.Errorf("the app had %v interrupts, want 2", r["interrupts"])
	}
}

// detachTest runs role in this test's helper processes: as the helper, it doesn't return. It's the path, less its
// suffix, the helpers report to.
func detachTest(t *testing.T, role string) string {
	if role := os.Getenv("ORIEL_DETACH_TEST"); role != "" {
		detachHelper(role, os.Getenv("ORIEL_DETACH_OUT"))
	}
	isolate.Test(t)
	out := filepath.Join(t.TempDir(), "report")
	t.Setenv("ORIEL_DETACH_TEST", role)
	t.Setenv("ORIEL_DETACH_OUT", out)
	t.Setenv(appEnv, "")
	os.Unsetenv(appEnv)
	return out
}

func detachHelper(role, out string) {
	if role == "restore" && os.Getenv(appEnv) == "" {
		superviseRestore(out)
	}
	fromTerminal := detach()
	switch role {
	case "notty":
		f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
		if err == nil {
			f.Close()
		}
		sid, _ := unix.Getsid(0)
		fmt.Println("the app says hello")
		report(out+".app", map[string]any{"fromTerminal": fromTerminal, "ttyOpens": err == nil,
			"stdinTerminal": tty.IsTerminal(os.Stdin), "ownSession": sid == os.Getpid(), "ppid": os.Getppid(),
			"marker": os.Getenv(appEnv)})
	case "restore":
		// The terminal by fd, as a program given one by path (GPG_TTY) would have it.
		term := os.NewFile(3, "tty")
		m, err := tty.Get(term)
		if err == nil {
			m.Lflag &^= unix.ISIG | unix.ICANON | unix.ECHO
			m.Iflag &^= unix.ICRNL
			err = tty.Set(term, m)
		}
		got, _ := tty.Get(term)
		report(out+".app", map[string]any{"raw": err == nil && got.Lflag&unix.ISIG == 0})
	case "signals":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		fmt.Println("ready")
		for n := 0; ; {
			if <-sig == os.Interrupt {
				n++
				fmt.Printf("interrupt %d\n", n)
				continue
			}
			report(out+".app", map[string]any{"interrupts": n})
			signal.Reset(syscall.SIGTERM)
			_ = unix.Kill(os.Getpid(), unix.SIGTERM)
			<-sig
		}
	}
	os.Exit(0)
}

// superviseRestore supervises the app with the terminal on its fd 3, and reports whether the modes it had before
// are back after.
func superviseRestore(out string) {
	before, err := tty.Get(os.Stdin)
	if err != nil {
		report(out+".supervisor", map[string]any{"err": err.Error()})
		os.Exit(1)
	}
	cmd, err := appCommand()
	if err != nil {
		report(out+".supervisor", map[string]any{"err": err.Error()})
		os.Exit(1)
	}
	cmd.ExtraFiles = []*os.File{os.Stdin}
	code := supervise(os.Stdin, cmd)
	after, _ := tty.Get(os.Stdin)
	report(out+".supervisor", map[string]any{"code": code, "restored": after != nil && *after == *before})
	os.Exit(0)
}

func report(path string, r map[string]any) {
	b, _ := json.Marshal(r)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func readReport(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no report: %v", err)
	}
	r := map[string]any{}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// readUntil reads p until its output holds want; the test's own timeout bounds it.
func readUntil(t *testing.T, p *pty.PTY, out *bytes.Buffer, want string) {
	t.Helper()
	buf := make([]byte, 1024)
	for !strings.Contains(out.String(), want) {
		n, err := p.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			t.Fatalf("reading for %q: %v; got %q", want, err, out.String())
		}
	}
}
