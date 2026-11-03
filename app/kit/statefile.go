package kit

import (
	"bytes"
	"context"
	"sync"
	"time"

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
// saved terminal screens. It's read through the app's freshness manager (watch.File) and written off the loop. Made
// as its plugin starts, it hands Load the file before it returns, so the plugin starts with its state; as the window
// moves it follows, holding the window's arrival on a directory (Window.Hold) until that directory's file is read
// and handed to Load. A reading that's one of its own writes, or the one last handed over, isn't handed back.
type StateFile struct {
	h     plugin.Host
	o     StateFileOpts
	m     *watch.Manager
	done  func() // closes m, when it's its own
	moved func()
	w     Writer

	mu      sync.Mutex
	path    string // the file followed now
	gen     int    // which subscription is the one followed now
	sub     watch.Sub[[]byte]
	release func()    // the window's hold, until the file followed now is first read
	wrote   [][]byte  // the last few writes, not handed back as changes
	handed  []byte    // the reading last handed to Load
	hasRead bool      // one has been, for the file followed now
	since   time.Time // when it began following it: its first reading is one read since
}

// firstReadLongest is how long a StateFile made as its plugin starts waits for its file.
const firstReadLongest = time.Second

// NewStateFile follows the window's directory's file for h.
func NewStateFile(h plugin.Host, o StateFileOpts) *StateFile {
	f := &StateFile{h: h, o: o}
	f.m, f.done = FileManager(h, "state", 0)
	gen := f.follow()
	f.mu.Lock()
	sub := f.sub
	f.mu.Unlock()
	if sub.Valid() {
		ctx, cancel := context.WithTimeout(context.Background(), firstReadLongest)
		sub.Fresh(ctx)
		cancel()
		f.reading(gen)
	}
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

// Path is the file followed now: the directory the window is on's; "" for none.
func (f *StateFile) Path() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.path
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
	f.done()
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

// follow subscribes to the file of the directory the window is on, holding its arrival until the first reading. It
// answers which subscription that is.
func (f *StateFile) follow() int {
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
	f.path, f.sub, f.release, f.wrote, f.handed, f.hasRead, f.since = path, watch.Sub[[]byte]{}, release, nil, nil, false, time.Now()
	if path != "" {
		f.sub = watch.SubscribeFunc(f.m, watch.File(path), watch.Need{Background: watch.Keep}, f.o.Name, func() {
			f.h.Do(func() { f.reading(gen) })
		})
	}
	sub := f.sub
	f.mu.Unlock()
	if sub.Valid() {
		sub.Refresh() // a reading kept from before may be older than the file: the first one handed over is read now
	}
	if old.Valid() {
		old.Close()
	}
	if oldRelease != nil {
		oldRelease()
	}
	if path == "" && f.o.Load != nil {
		f.o.Load(nil)
	}
	return gen
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
	if !f.hasRead {
		data, _, ok = f.sub.Since(f.since)
	}
	release := f.release
	f.release = nil
	same := ok && f.hasRead && (bytes.Equal(data, f.handed) || f.isMine(data))
	if ok && !same {
		f.handed, f.hasRead = bytes.Clone(data), true
	}
	f.mu.Unlock()
	if ok && !same && f.o.Load != nil {
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
