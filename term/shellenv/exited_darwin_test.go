package shellenv

import (
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// exited waits for process pid to end, giving up after d: false if it's still running then. A process already gone
// has ended.
func exited(pid int, d time.Duration) bool {
	kq, err := unix.Kqueue()
	if err != nil {
		return false
	}
	defer unix.Close(kq)
	ev := []unix.Kevent_t{{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_ADD | unix.EV_ONESHOT, Fflags: unix.NOTE_EXIT}}
	if _, err := unix.Kevent(kq, ev, nil, nil); err != nil {
		return errors.Is(err, unix.ESRCH)
	}
	ts := unix.NsecToTimespec(d.Nanoseconds())
	out := make([]unix.Kevent_t, 1)
	for {
		n, err := unix.Kevent(kq, nil, out, &ts)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		return n > 0
	}
}
