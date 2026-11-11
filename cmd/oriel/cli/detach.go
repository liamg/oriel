package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/liamg/oriel/term/tty"
)

// appEnv marks the app's process, started by a supervisor on a terminal.
const appEnv = "ORIEL_FROM_TERMINAL"

// forwarded are the signals the supervisor passes to the app, one for each it gets: ^C, ^\, kill, the terminal
// closing.
var forwarded = []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT}

// detach says whether the app was started from a terminal. From one, this process becomes the app's supervisor and
// exits when the app does: the app runs as its child on a session of its own, with no controlling terminal, so
// nothing it runs can open /dev/tty and leave the terminal raw (a prompt killed mid-way), and ^C still reaches it.
// A program handed the terminal by path (GPG_TTY) can still open it.
func detach() (fromTerminal bool) {
	if os.Getenv(appEnv) != "" {
		os.Unsetenv(appEnv)
		return true
	}
	if !tty.IsTerminal(os.Stdin) {
		return false
	}
	cmd, err := appCommand()
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		os.Exit(1)
	}
	os.Exit(supervise(os.Stdin, cmd))
	return false
}

// appCommand runs this program again, with the same arguments, as the app: on a session of its own, reading
// /dev/null, printing to this process's stdout and stderr.
func appCommand() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Args[0] = os.Args[0]
	cmd.Env = append(os.Environ(), appEnv+"=1")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd, nil
}

// supervise runs cmd, passes it the forwarded signals, and once it has ended puts term's modes back as they were.
// It's cmd's exit code, or 128+n for a death by signal n.
func supervise(term *os.File, cmd *exec.Cmd) int {
	modes, err := tty.Get(term)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		return 1
	}
	sig := make(chan os.Signal, len(forwarded))
	signal.Notify(sig, forwarded...)
	defer signal.Stop(sig)
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		return 1
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		select {
		case s := <-sig:
			_ = cmd.Process.Signal(s)
		case err := <-done:
			if tty.Foreground(term) {
				_ = tty.Set(term, modes)
			}
			return exitCode(err)
		}
	}
}

// exitCode is how a process ended as a shell reports it.
func exitCode(err error) int {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		if err != nil {
			return 1
		}
		return 0
	}
	if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	return exit.ExitCode()
}
