package fswatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

const (
	dirNotes  = unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE
	fileNotes = unix.NOTE_WRITE | unix.NOTE_EXTEND | unix.NOTE_ATTRIB | unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE
	goneNotes = unix.NOTE_DELETE | unix.NOTE_RENAME | unix.NOTE_REVOKE
)

// opened counts the descriptors every watcher holds, kept under half the process's limit.
var opened atomic.Int64

var fdBudget = sync.OnceValue(func() int64 {
	var lim unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &lim); err != nil || lim.Cur == 0 {
		return 128
	}
	return int64(min(lim.Cur, 1<<20) / 2)
})

// kqueue holds a descriptor per watched folder and file. A folder's note says only that its entries changed, so the
// names it had are kept to tell what was created and removed.
type kqueue struct {
	fd   int
	wake [2]int

	mu      sync.Mutex
	stopped bool
	byFD    map[int]*node
	byPath  map[string]*node
}

type node struct {
	fd    int
	path  string
	dir   bool
	names map[string]bool // a folder's entries: name → is a folder
}

func newBackend() (backend, error) {
	fd, err := unix.Kqueue()
	if err != nil {
		return nil, limitErr(err)
	}
	unix.CloseOnExec(fd)
	b := &kqueue{fd: fd, byFD: map[int]*node{}, byPath: map[string]*node{}}
	if err := unix.Pipe(b.wake[:]); err != nil {
		unix.Close(fd)
		return nil, err
	}
	unix.CloseOnExec(b.wake[0])
	unix.CloseOnExec(b.wake[1])
	var ev [1]unix.Kevent_t
	unix.SetKevent(&ev[0], b.wake[0], unix.EVFILT_READ, unix.EV_ADD)
	if _, err := unix.Kevent(fd, ev[:], nil, nil); err != nil {
		unix.Close(fd)
		unix.Close(b.wake[0])
		unix.Close(b.wake[1])
		return nil, err
	}
	return b, nil
}

func limitErr(err error) error {
	if errors.Is(err, unix.EMFILE) || errors.Is(err, unix.ENFILE) || errors.Is(err, unix.ENOMEM) {
		return fmt.Errorf("%w: %v", ErrLimit, err)
	}
	return err
}

func (b *kqueue) watchDir(p string) error  { return b.watch(p, true) }
func (b *kqueue) watchFile(p string) error { return b.watch(p, false) }

func (b *kqueue) watch(p string, dir bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.byPath[p]; ok || b.stopped {
		return nil
	}
	if opened.Add(1) > fdBudget() {
		opened.Add(-1)
		return fmt.Errorf("%w: %d descriptors open", ErrLimit, fdBudget())
	}
	fd, err := unix.Open(p, unix.O_EVTONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		opened.Add(-1)
		return limitErr(&fs.PathError{Op: "watch", Path: p, Err: err})
	}
	n := &node{fd: fd, path: p, dir: dir}
	notes := uint32(fileNotes)
	if dir {
		n.names = names(p)
		notes = dirNotes
	}
	var ev [1]unix.Kevent_t
	unix.SetKevent(&ev[0], fd, unix.EVFILT_VNODE, unix.EV_ADD|unix.EV_CLEAR)
	ev[0].Fflags = notes
	if _, err := unix.Kevent(b.fd, ev[:], nil, nil); err != nil {
		unix.Close(fd)
		opened.Add(-1)
		return limitErr(&fs.PathError{Op: "watch", Path: p, Err: err})
	}
	b.byFD[fd] = n
	b.byPath[p] = n
	return nil
}

func names(dir string) map[string]bool {
	ents, _ := os.ReadDir(dir)
	m := make(map[string]bool, len(ents))
	for _, e := range ents {
		m[e.Name()] = e.IsDir()
	}
	return m
}

// drop closes n's descriptor, which takes it out of the kqueue; b.mu is held.
func (b *kqueue) drop(n *node) {
	unix.Close(n.fd)
	opened.Add(-1)
	delete(b.byFD, n.fd)
	delete(b.byPath, n.path)
}

func (b *kqueue) unwatch(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n, ok := b.byPath[p]; ok {
		b.drop(n)
	}
}

func (b *kqueue) unwatchUnder(p string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for q, n := range b.byPath {
		if under(q, p) {
			b.drop(n)
		}
	}
}

func (b *kqueue) watched() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.byPath))
	for p := range b.byPath {
		out = append(out, p)
	}
	return out
}

func (b *kqueue) close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return nil
	}
	_, err := unix.Write(b.wake[1], []byte{0})
	return err
}

func (b *kqueue) run(handle func([]raw)) {
	defer func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		b.stopped = true
		for _, n := range b.byPath {
			b.drop(n)
		}
		unix.Close(b.fd)
		unix.Close(b.wake[0])
		unix.Close(b.wake[1])
	}()
	evs := make([]unix.Kevent_t, 64)
	for {
		n, err := unix.Kevent(b.fd, nil, evs, nil)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			return
		}
		var out []raw
		for _, ev := range evs[:n] {
			if int(ev.Ident) == b.wake[0] {
				return
			}
			out = append(out, b.note(int(ev.Ident), ev.Fflags)...)
		}
		if len(out) > 0 {
			handle(out)
		}
	}
}

// note turns a descriptor's note into changes.
func (b *kqueue) note(fd int, fflags uint32) []raw {
	b.mu.Lock()
	defer b.mu.Unlock()
	n, ok := b.byFD[fd]
	if !ok {
		return nil
	}
	if fflags&goneNotes != 0 {
		return []raw{{path: n.path, dir: n.dir, op: opRemoved}}
	}
	if !n.dir && fflags&(unix.NOTE_WRITE|unix.NOTE_EXTEND) == 0 {
		return []raw{{path: n.path, op: opAttrib}}
	}
	if !n.dir {
		return []raw{{path: n.path, op: opChanged}}
	}
	now := names(n.path)
	var out []raw
	for name, isDir := range now {
		if was, ok := n.names[name]; !ok || was != isDir {
			out = append(out, raw{path: filepath.Join(n.path, name), dir: isDir, op: opCreated})
		}
	}
	for name, isDir := range n.names {
		if _, ok := now[name]; !ok {
			out = append(out, raw{path: filepath.Join(n.path, name), dir: isDir, op: opRemoved})
		}
	}
	n.names = now
	return out
}
