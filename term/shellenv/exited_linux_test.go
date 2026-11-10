package shellenv

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// exited waits for process pid to end, giving up after d: false if it's still running then. A process already gone
// has ended.
func exited(pid int, d time.Duration) bool {
	fd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return errors.Is(err, unix.ESRCH)
	}
	defer unix.Close(fd)
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(d.Milliseconds()))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return n > 0
	}
}
