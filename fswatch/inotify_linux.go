package fswatch

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

const dirMask = unix.IN_CREATE | unix.IN_DELETE | unix.IN_MODIFY | unix.IN_MOVED_FROM | unix.IN_MOVED_TO |
	unix.IN_DELETE_SELF | unix.IN_MOVE_SELF | unix.IN_ONLYDIR | unix.IN_EXCL_UNLINK

// inotify watches folders; a folder's watch also reports writes to the files in it, so files need none of their own.
type inotify struct {
	fd   int
	wake [2]int

	mu      sync.Mutex
	stopped bool
	wds     map[int32]string
	paths   map[string]int32
}

func newBackend() (backend, error) {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return nil, limitErr(err)
	}
	b := &inotify{fd: fd, wds: map[int32]string{}, paths: map[string]int32{}}
	if err := unix.Pipe2(b.wake[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return b, nil
}

func limitErr(err error) error {
	if errors.Is(err, unix.ENOSPC) || errors.Is(err, unix.EMFILE) || errors.Is(err, unix.ENFILE) {
		return fmt.Errorf("%w: %v", ErrLimit, err)
	}
	return err
}

func (b *inotify) watchDir(p string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.paths[p]; ok || b.stopped {
		return nil
	}
	wd, err := unix.InotifyAddWatch(b.fd, p, dirMask)
	if err != nil {
		return limitErr(&fs.PathError{Op: "watch", Path: p, Err: err})
	}
	b.wds[int32(wd)] = p
	b.paths[p] = int32(wd)
	return nil
}

func (b *inotify) watchFile(string) error { return nil }

func (b *inotify) unwatch(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if wd, ok := b.paths[p]; ok && !b.stopped {
		_, _ = unix.InotifyRmWatch(b.fd, uint32(wd))
		delete(b.paths, p)
		delete(b.wds, wd)
	}
}

func (b *inotify) unwatchUnder(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	for q, wd := range b.paths {
		if under(q, p) {
			_, _ = unix.InotifyRmWatch(b.fd, uint32(wd))
			delete(b.paths, q)
			delete(b.wds, wd)
		}
	}
}

func (b *inotify) watched() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.paths))
	for p := range b.paths {
		out = append(out, p)
	}
	return out
}

func (b *inotify) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return nil
	}
	_, err := unix.Write(b.wake[1], []byte{0})
	return err
}

func (b *inotify) run(handle func([]raw)) {
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.stopped = true
		unix.Close(b.fd)
		unix.Close(b.wake[0])
		unix.Close(b.wake[1])
	}()
	buf := make([]byte, 64<<10)
	fds := []unix.PollFd{{Fd: int32(b.fd), Events: unix.POLLIN}, {Fd: int32(b.wake[0]), Events: unix.POLLIN}}
	for {
		if _, err := unix.Poll(fds, -1); err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		if fds[1].Revents != 0 {
			return
		}
		n, err := unix.Read(b.fd, buf)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			return
		}
		if evs := b.parse(buf[:n]); len(evs) > 0 {
			handle(evs)
		}
	}
}

func (b *inotify) parse(buf []byte) []raw {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []raw
	for off := 0; off+unix.SizeofInotifyEvent <= len(buf); {
		ev := (*unix.InotifyEvent)(unsafe.Pointer(&buf[off]))
		nameAt := off + unix.SizeofInotifyEvent
		end := nameAt + int(ev.Len)
		if end > len(buf) {
			break
		}
		name := string(buf[nameAt:end])
		for i := 0; i < len(name); i++ {
			if name[i] == 0 {
				name = name[:i]
				break
			}
		}
		off = end
		m := ev.Mask
		if m&unix.IN_Q_OVERFLOW != 0 {
			out = append(out, raw{op: opOverflow})
			continue
		}
		dir, ok := b.wds[ev.Wd]
		if !ok {
			continue
		}
		if m&unix.IN_IGNORED != 0 {
			delete(b.wds, ev.Wd)
			if b.paths[dir] == ev.Wd {
				delete(b.paths, dir)
			}
			continue
		}
		p := dir
		if name != "" {
			p = filepath.Join(dir, name)
		}
		isDir := m&unix.IN_ISDIR != 0
		switch {
		case m&(unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0:
			out = append(out, raw{path: dir, dir: true, op: opRemoved})
		case m&(unix.IN_CREATE|unix.IN_MOVED_TO) != 0:
			out = append(out, raw{path: p, dir: isDir, op: opCreated})
		case m&(unix.IN_DELETE|unix.IN_MOVED_FROM) != 0:
			out = append(out, raw{path: p, dir: isDir, op: opRemoved})
		default:
			out = append(out, raw{path: p, dir: isDir, op: opChanged})
		}
	}
	return out
}
