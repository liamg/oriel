package tty

import "golang.org/x/sys/unix"

const (
	getModes = unix.TIOCGETA
	setModes = unix.TIOCSETA
)
