package kit

import (
	"bytes"
	"sync"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/watch"
)

// StateFileOpts are what a StateFile keeps, and where.
type StateFileOpts struct {
	Name string        // what it is, for the Performance page: "debug"
	Path func() string // the file for the directory the window is on now ("" for none): the shell's Parts.DirState
	// Load gets, on the loop, what's kept for the window's directory: nil for nothing. It's called once the file's
	// read, as the window starts or moves onto a directory, and again when something else changes the file.
	Load func(data []byte)
	// Leave gives, on the loop as the window leaves a directory, what to keep for it; nil keeps what was written.
	Leave func() []byte
}

// StateFile is a file a plugin keeps for the directory the window is on: a debugger's breakpoints, a directory's
// saved terminal screens. It's read through the app's freshness manager (watch.File), never on the loop, and
// written off it. It follows the window as it moves, holding the window's arrival on a directory (Window.Hold) until
// that directory's file is read and handed to Load. A reading that's one of its own writes isn't handed back.
type StateFile struct {
	h     plugin.Host
	o     StateFileOpts
	m     *watch.Manager
	own   bool // m is its own, with no window's to share
	moved func()
	w     Writer

	mu      sync.Mutex
	path    string // the file followed now
	gen     int    // which subscription is the one followed now
	sub     watch.Sub[[]byte]
	release func()   // the window's hold, until the file followed now is first read
	wrote   [][]byte // the last few writes, not handed back as changes
}

// NewStateFile follows the window's directory's file for h.
func NewStateFile(h plugin.Host, o StateFileOpts) *StateFile {
	f := &StateFile{h: h, o: o}
	if f.m = ManagerFor(h, ""); f.m == nil {
		f.m, f.own = watch.New(watch.Options{Name: "state", Polls: PollsOf(h)}), true
	}
	f.follow()
	f.moved = OnDirChanged(h, "the "+o.Name+" kept for the directory", func(DirChanged) { f.leave() }).Dispose
	return f
}

// Write keeps data as the file of the directory the window is on, off the loop.
func (f *StateFile) Write(data []byte) {
	f.mu.Lock()
	path := f.path
	f.wrote = append(f.wrote, bytes.Clone(data))
	if len(f.wrote) > 4 {
		f.wrote = f.wrote[1:]
	}
	f.mu.Unlock()
	if path != "" {
		f.w.Write(path, data)
	}
}

// Wait waits for the writes under way.
func (f *StateFile) Wait() { f.w.Wait() }

// Close stops following, once the writes under way are done.
func (f *StateFile) Close() {
	f.moved()
	f.mu.Lock()
	sub, release := f.sub, f.release
	f.sub, f.release = watch.Sub[[]byte]{}, nil
	f.mu.Unlock()
	if sub.Valid() {
		sub.Close()
	}
	if release != nil {
		release()
	}
	f.w.Wait()
	if f.own {
		f.m.Close()
	}
}

// leave keeps what Leave gives for the directory the window is leaving, and follows the new one's file.
func (f *StateFile) leave() {
	if f.o.Leave != nil {
		if data := f.o.Leave(); data != nil {
			f.Write(data)
		}
	}
	f.follow()
}

// follow subscribes to the file of the directory the window is on, holding its arrival until the first reading.
func (f *StateFile) follow() {
	path := ""
	if f.o.Path != nil {
		path = f.o.Path()
	}
	var release func()
	if w, ok := WindowOf(f.h); ok && path != "" {
		release = w.Hold()
	}
	f.mu.Lock()
	old, oldRelease := f.sub, f.release
	f.gen++
	gen := f.gen
	f.path, f.sub, f.release, f.wrote = path, watch.Sub[[]byte]{}, release, nil
	if path != "" {
		f.sub = watch.SubscribeFunc(f.m, watch.File(path), watch.Need{Background: watch.Keep}, f.o.Name, func() {
			f.h.Do(func() { f.reading(gen) })
		})
	}
	f.mu.Unlock()
	if old.Valid() {
		old.Close()
	}
	if oldRelease != nil {
		oldRelease()
	}
	if path == "" && f.o.Load != nil {
		f.o.Load(nil)
	}
}

// reading hands Load the file's latest reading, on the loop, unless it's of a file left since or one of its own
// writes; the first lets the window's arrival go.
func (f *StateFile) reading(gen int) {
	f.mu.Lock()
	if gen != f.gen {
		f.mu.Unlock()
		return
	}
	data, _, ok := f.sub.Get()
	release := f.release
	f.release = nil
	mine := release == nil && ok && f.isMine(data)
	f.mu.Unlock()
	if ok && !mine && f.o.Load != nil {
		f.o.Load(data)
	}
	if release != nil {
		release()
	}
}

// isMine says data is one of its own recent writes. Callers hold f.mu.
func (f *StateFile) isMine(data []byte) bool {
	for _, w := range f.wrote {
		if bytes.Equal(w, data) {
			return true
		}
	}
	return false
}
