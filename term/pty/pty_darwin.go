package pty

import (
	"bytes"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

func open() (master, slave *os.File, err error) {
	// Blocking: kqueue doesn't report readiness on macOS pseudo-terminals reliably.
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYGRANT, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCPTYUNLK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	var buf [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		master.Close()
		return nil, nil, errno
	}
	name := string(buf[:bytes.IndexByte(buf[:], 0)])
	sfd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, os.NewFile(uintptr(sfd), name), nil
}

// hangup sends SIGHUP to the terminal's foreground group, as the kernel would on it closing: a blocking Read in
// progress holds the terminal open, so closing it alone sends nothing.
func (p *PTY) hangup() {
	if pg, err := unix.IoctlGetInt(int(p.f.Fd()), unix.TIOCGPGRP); err == nil && pg > 0 {
		_ = unix.Kill(-pg, unix.SIGHUP)
	}
}
