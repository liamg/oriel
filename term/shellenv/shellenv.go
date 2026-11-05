// Package shellenv reads the PATH the user's login shell sets up. An app opened from the Finder, the Dock or a desktop
// launcher inherits the session's bare PATH, not the one the user's shell config builds, so the programs it runs
// (claude, codex, git from a package manager) aren't found.
package shellenv

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/liamg/oriel/term/pty"
)

// Resolving is set in the shell's environment while it's read, so its config can skip what's slow or interactive.
const Resolving = "ORIEL_RESOLVING_ENVIRONMENT"

const (
	begin = "__ORIEL_SHELL_PATH_BEGIN__"
	end   = "__ORIEL_SHELL_PATH_END__"
)

// script prints PATH between the markers. printenv, not $PATH: a list in fish, joined by colons in what it exports;
// by its path, which macOS and Linux both have it at, so it's found whatever PATH the config left.
const script = "printf '%s' '" + begin + "'; /usr/bin/printenv PATH; printf '%s' '" + end + "'"

// Timeout is how long the shell is given to start and answer.
const Timeout = 5 * time.Second

// Shell is the user's login shell: $SHELL, else the one their account names, else sh.
func Shell() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if s := accountShell(); s != "" {
		return s
	}
	return "/bin/sh"
}

// Path is the PATH shell sets up as an interactive login shell, its config files read: what a terminal opened now
// would have.
func Path(ctx context.Context, shell string, timeout time.Duration) (string, error) {
	var err error
	for _, flags := range [][]string{{"-i", "-l", "-c"}, {"-l", "-c"}} { // csh takes -l only alone
		var p string
		if p, err = run(ctx, shell, timeout, append(flags, script)...); err == nil {
			return p, nil
		}
		if ctx.Err() != nil {
			break
		}
	}
	return "", err
}

// run runs shell with args on a terminal of its own, as it would run in one (some shells, and some configs, can't
// start a program without one), and reads the PATH it prints. It stops at the PATH, or after timeout.
func run(ctx context.Context, shell string, timeout time.Duration, args ...string) (string, error) {
	cmd := exec.Command(shell, args...)
	cmd.Env = append(os.Environ(), Resolving+"=1")
	if os.Getenv("TERM") == "" {
		cmd.Env = append(cmd.Env, "TERM=dumb") // nothing to draw a prompt theme on
	}
	p, err := pty.Start(cmd, 200, 50)
	if err != nil {
		return "", err
	}
	found := make(chan string, 1)
	read := make(chan struct{})
	var out bytes.Buffer
	go func() { // read to the end, after the PATH too: macOS won't let a process exit with its output unread
		defer close(read)
		buf := make([]byte, 4096)
		told := false
		for {
			n, err := p.Read(buf)
			if !told {
				out.Write(buf[:n])
				if path, ok := between(out.String()); ok {
					found <- path
					told = true
				}
			}
			if err != nil {
				return
			}
		}
	}()
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	// stop kills the shell's session and lets the terminal go, never waiting long: this runs as the app starts.
	stop := func() {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) // the shell leads a session of its own: it, and what it started
		p.Close()                                           // what it hadn't written yet goes, so it can exit
		for _, done := range []<-chan struct{}{waited(exited), read} {
			select {
			case <-done:
			case <-time.After(time.Second): // a child that left the shell's group may hold the terminal open
			}
		}
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case path := <-found:
		stop()
		return checked(shell, path)
	case err := <-exited:
		exited <- err
		select {
		case path := <-found:
			stop()
			return checked(shell, path)
		case <-read:
		case <-time.After(200 * time.Millisecond):
		}
		stop()
		if err == nil {
			err = errors.New(filepath.Base(shell) + " didn't print its PATH")
		}
		return "", err
	case <-timer.C:
	case <-ctx.Done():
	}
	stop()
	return "", errors.New(filepath.Base(shell) + " didn't answer in " + timeout.String())
}

// waited closes once exited has an answer, putting it back.
func waited(exited chan error) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		err := <-exited
		exited <- err
		close(done)
	}()
	return done
}

// between is what the shell printed between the markers, once it has printed both.
func between(s string) (string, bool) {
	i := strings.LastIndex(s, begin)
	if i < 0 {
		return "", false
	}
	j := strings.Index(s[i:], end)
	if j < 0 {
		return "", false
	}
	return strings.TrimSpace(s[i+len(begin) : i+j]), true
}

// checked is path if it reads as one: directories by their absolute paths, separated by colons. The terminal the shell
// runs on carries its errors too, so what it printed between the markers may be one.
func checked(shell, path string) (string, error) {
	if path == "" {
		return "", errors.New(filepath.Base(shell) + " has no PATH")
	}
	for _, d := range filepath.SplitList(path) {
		if d != "" && !filepath.IsAbs(d) || strings.ContainsAny(d, "\r\n") {
			return "", fmt.Errorf("%s printed %q, not a PATH", filepath.Base(shell), path)
		}
	}
	return path, nil
}

// Merge is the shell's PATH, then what's in current that it lacks, each directory once.
func Merge(shell, current string) string {
	var out []string
	for _, d := range append(filepath.SplitList(shell), filepath.SplitList(current)...) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// Apply sets the process's PATH to the login shell's, keeping what it had besides. It says what it set.
func Apply(ctx context.Context) (string, error) {
	p, err := Path(ctx, Shell(), Timeout)
	if err != nil {
		return "", err
	}
	merged := Merge(p, os.Getenv("PATH"))
	return merged, os.Setenv("PATH", merged)
}

// FromTerminal says the process was started from a terminal, so it has the shell's environment already.
func FromTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
