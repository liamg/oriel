package tty

import "golang.org/x/sys/unix"

const (
	getModes = unix.TCGETS
	setModes = unix.TCSETS
)
