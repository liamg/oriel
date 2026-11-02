package pty

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func open() (master, slave *os.File, err error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	// Non-blocking, so the runtime poller serves reads and Close interrupts one in progress.
	master = os.NewFile(uintptr(fd), "/dev/ptmx")
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		master.Close()
		return nil, nil, err
	}
	n, err := unix.IoctlGetUint32(fd, unix.TIOCGPTN)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	name := "/dev/pts/" + strconv.Itoa(int(n))
	sfd, err := unix.Open(name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, os.NewFile(uintptr(sfd), name), nil
}

// hangup is closing the terminal's: the kernel sends SIGHUP as Close ends a Read in progress.
func (p *PTY) hangup() {}
