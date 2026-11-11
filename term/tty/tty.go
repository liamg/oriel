//go:build linux || darwin

// Package tty reads and sets a terminal's modes, over golang.org/x/sys/unix, on Linux and macOS.
package tty

import (
	"os"

	"golang.org/x/sys/unix"
)

// Modes are a terminal's settings: line discipline, echo, the keys that send signals.
type Modes = unix.Termios

// IsTerminal says f is a terminal: /dev/null is a character device too, but has no modes.
func IsTerminal(f *os.File) bool {
	_, err := Get(f)
	return err == nil
}

// Get reads the terminal's modes.
func Get(f *os.File) (*Modes, error) { return unix.IoctlGetTermios(int(f.Fd()), getModes) }

// Set changes the terminal's modes now.
func Set(f *os.File, m *Modes) error { return unix.IoctlSetTermios(int(f.Fd()), setModes, m) }

// Same says a and b are the same settings, past what the kernel sets of its own accord: macOS marks input pending
// (PENDIN) when a terminal goes back to lines, so the modes read back after a restore have it.
func Same(a, b *Modes) bool {
	x, y := *a, *b
	x.Lflag &^= unix.PENDIN
	y.Lflag &^= unix.PENDIN
	return x == y
}

// Foreground says this process's group is the terminal's foreground group. One in the background that sets the
// modes is stopped, and would change them under the shell.
func Foreground(f *os.File) bool {
	pg, err := unix.IoctlGetInt(int(f.Fd()), unix.TIOCGPGRP)
	return err == nil && pg == unix.Getpgrp()
}
