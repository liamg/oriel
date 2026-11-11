package kit

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/ui"
)

// ReadsService is the window's Reads.
const ReadsService = "reads"

// Reads are what the window's views read from disk and the web, each through a plugin.Cache: files, folders and
// pictures. A read never blocks the frame: it starts off the loop and lands at the next ask, which its redraw brings.
// Their methods are called on the loop.
type Reads struct {
	Files    *plugin.Cache[string, plugin.File]
	Dirs     *plugin.Cache[string, []fs.DirEntry]
	Stats    *plugin.Cache[string, fs.FileInfo]
	Reals    *plugin.Cache[string, string] // paths with symbolic links resolved
	Programs *plugin.Cache[string, string] // programs on PATH, by name (exec.LookPath)
	// Highlights are windows of files' text highlighted, each made off the loop for one version of the text.
	Highlights *plugin.Cache[highlightKey, *SourceFile]
	Pictures   *Pictures
	q          *landing
	frame      func() int
	asked      int // the frame a read last came back pending in
}

// ReadsOpts are how a window's reads are made.
type ReadsOpts struct {
	Client *http.Client     // what remote pictures are fetched with; public addresses only if nil
	Wake   func()           // asks for a frame as a read lands
	Frame  func() int       // the frame being built; reads not asked for since are cancelled. None if nil
	Now    func() time.Time // the window's clock; time.Now if nil
	Watch  func() (*fswatch.Watcher, error)
}

func NewReads(o ReadsOpts) *Reads {
	if o.Wake == nil {
		o.Wake = func() {}
	}
	if o.Frame == nil {
		o.Frame = func() int { return 0 }
	}
	q := &landing{wake: o.Wake}
	r := &Reads{q: q, frame: o.Frame, asked: -1}
	r.Files = plugin.NewFiles(q, plugin.CacheOpts[string, plugin.File]{Frame: o.Frame, Now: o.Now, Watch: o.Watch})
	r.Dirs = plugin.NewDirs(q, plugin.CacheOpts[string, []fs.DirEntry]{Frame: o.Frame, Now: o.Now, Watch: o.Watch})
	r.Stats = plugin.NewCache(q, func(_ context.Context, p string) (fs.FileInfo, error) { return os.Stat(p) },
		plugin.CacheOpts[string, fs.FileInfo]{Frame: o.Frame, Now: o.Now, Watch: o.Watch, Path: func(p string) string { return p }})
	r.Reals = plugin.NewCache(q, func(_ context.Context, p string) (string, error) { return filepath.EvalSymlinks(p) },
		plugin.CacheOpts[string, string]{Frame: o.Frame, Now: o.Now, MaxAge: 10 * time.Second})
	r.Programs = plugin.NewCache(q, func(_ context.Context, name string) (string, error) { return exec.LookPath(name) },
		plugin.CacheOpts[string, string]{Frame: o.Frame, Now: o.Now, MaxAge: time.Minute})
	r.Highlights = plugin.NewCache(q, func(_ context.Context, k highlightKey) (*SourceFile, error) {
		return highlightIn(k.lang, k.path, k.src.b, k.line, k.before, k.after), nil
	}, plugin.CacheOpts[highlightKey, *SourceFile]{MaxEntries: 64, Frame: o.Frame, Now: o.Now})
	r.Pictures = newPictures(q, o)
	r.Pictures.note = r.note
	return r
}

// ReadsOf is the window's reads.
func ReadsOf(r registry.Registry) (*Reads, bool) {
	if r == nil {
		return nil, false
	}
	return registry.ServiceOf[*Reads](r, ReadsService)
}

// ReadsIn are the reads c's UI was given; its own for a UI that wasn't.
func ReadsIn(c *ui.Ctx) *Reads {
	return ui.Keep(c, func() *Reads {
		return NewReads(ReadsOpts{Watch: func() (*fswatch.Watcher, error) { return nil, fswatch.ErrUnsupported }})
	})
}

// File is path's text and its read's state.
func (r *Reads) File(path string) (plugin.File, ui.LoadState) {
	r.q.run()
	f, st := r.Files.Get(path)
	r.note(st)
	return f, st
}

// Dir is the folder's entries and their read's state.
func (r *Reads) Dir(path string) ([]fs.DirEntry, ui.LoadState) {
	r.q.run()
	d, st := r.Dirs.Get(path)
	r.note(st)
	return d, st
}

// Stat is what the OS says of the file at path, and its read's state.
func (r *Reads) Stat(path string) (fs.FileInfo, ui.LoadState) {
	r.q.run()
	fi, st := r.Stats.Get(path)
	r.note(st)
	return fi, st
}

// Within is kit.Within with symbolic links resolved through the cache: as written until they are.
func (r *Reads) Within(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	if Inside(path, dir) {
		return true
	}
	r.q.run()
	rp, st1 := r.Reals.Get(path)
	rd, st2 := r.Reals.Get(dir)
	r.note(st1)
	r.note(st2)
	return rp != "" && rd != "" && Inside(rp, rd)
}

// Has says the program name is on PATH; false until it's looked up.
func (r *Reads) Has(name string) bool {
	r.q.run()
	p, st := r.Programs.Get(name)
	r.note(st)
	return p != ""
}

// highlightKey is a window of one version of a file's text, in its language.
type highlightKey struct {
	path                string
	src                 *srcBytes
	lang                *syntax.Language
	line, before, after int
}

// srcBytes is one version of a text: a new one each time it changes.
type srcBytes struct{ b []byte }

// highlight is the window k highlighted, nil until it's made.
func (r *Reads) highlight(k highlightKey) *SourceFile {
	r.q.run()
	f, st := r.Highlights.Get(k)
	r.note(st)
	return f
}

func (r *Reads) note(st ui.LoadState) {
	if st.Phase == ui.Pending {
		r.asked = r.frame()
	}
}

// Busy says whether a read from disk the latest frame asked for is still under way, or one has landed it hasn't
// shown. A remote picture's fetch doesn't count.
func (r *Reads) Busy() bool { return r.asked == r.frame() || r.q.queued() }

// Pending names the reads under way, for a test that never settles.
func (r *Reads) Pending() []string {
	var out []string
	for _, p := range r.Files.Fetching() {
		out = append(out, "file "+p)
	}
	for _, p := range r.Dirs.Fetching() {
		out = append(out, "folder "+p)
	}
	for _, p := range r.Stats.Fetching() {
		out = append(out, "stat "+p)
	}
	for _, p := range r.Reals.Fetching() {
		out = append(out, "real path of "+p)
	}
	for _, n := range r.Programs.Fetching() {
		out = append(out, "program "+n)
	}
	for _, k := range r.Highlights.Fetching() {
		out = append(out, fmt.Sprintf("highlighting %s:%d", k.path, k.line))
	}
	slices.Sort(out)
	if r.q.queued() {
		out = append(out, "a read landed and not shown")
	}
	if len(out) == 0 && r.asked == r.frame() {
		out = append(out, "a read the latest frame found pending, landed since")
	}
	return out
}

// Close cancels the reads under way and stops watching.
func (r *Reads) Close() {
	r.Files.Close()
	r.Dirs.Close()
	r.Stats.Close()
	r.Reals.Close()
	r.Programs.Close()
	r.Highlights.Close()
	r.Pictures.Close()
}

// landing is the loop the reads' results land on: queued as they come, and run at the next ask.
type landing struct {
	mu   sync.Mutex
	fns  []func()
	wake func()
}

func (l *landing) Do(fn func()) {
	l.mu.Lock()
	l.fns = append(l.fns, fn)
	l.mu.Unlock()
	l.wake()
}

func (l *landing) Redraw() { l.wake() }

func (l *landing) run() {
	l.mu.Lock()
	fns := l.fns
	l.fns = nil
	l.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (l *landing) queued() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.fns) > 0
}
