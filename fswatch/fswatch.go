// Package fswatch tells when files and folders change, from the OS (inotify on Linux; on macOS FSEvents for trees and
// kqueue for the rest) rather than by polling. Changes are coalesced: a burst of writes is one event per path.
//
// A watch that can't be set up (the OS's limits, an unsupported platform) is an error; the caller polls instead.
package fswatch

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// Delay is how long changes are gathered before they're sent.
const Delay = 75 * time.Millisecond

// ErrLimit is a watch the OS has no room for: too many watches or open files.
var ErrLimit = errors.New("fswatch: out of watches")

// ErrUnsupported is a platform with no watcher.
var ErrUnsupported = errors.New("fswatch: not supported on this platform")

// Event is a path that changed: written, created, removed or renamed.
type Event struct {
	Path string
	// Err is set when the watch under Path stopped working (the OS ran out of watches); poll it from now on.
	Err error
}

// Watcher watches paths and sends what changed on Events.
type Watcher struct {
	b    backend
	out  chan Event
	kick chan struct{}
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once

	mu      sync.Mutex
	roots   map[string]*root
	pending map[string]error
	seen    map[string]os.FileInfo // each file as it was at its last event, to tell a metadata-only one
}

// root is what the caller added.
type root struct {
	path   string
	tree   bool
	skip   func(rel string) bool
	anchor string // the folder watched for it: path or its parent, or the nearest that exists
	dir    bool   // path is a folder
	stream *stream
}

// walked is a tree whose folders and files are each watched.
func (r *root) walked() bool { return r.tree && r.anchor == r.path && r.stream == nil }

type op int

const (
	opChanged op = iota
	opAttrib     // a file's metadata, not its content
	opCreated
	opRemoved
	opOverflow
)

// raw is one change from the OS.
type raw struct {
	path string
	dir  bool
	op   op
}

// backend is the OS's watcher. A watched folder reports its entries being created, removed and renamed (and on Linux
// written); a watched file its writes. Watching again is a no-op.
type backend interface {
	watchDir(path string) error
	watchFile(path string) error
	unwatch(path string)
	unwatchUnder(path string)
	watched() []string
	run(handle func([]raw))
	close() error
}

// New starts a watcher. It fails with ErrUnsupported or ErrLimit when the OS can't give one.
func New() (*Watcher, error) {
	b, err := newBackend()
	if err != nil {
		return nil, err
	}
	return newWatcher(b), nil
}

func newWatcher(b backend) *Watcher {
	w := &Watcher{b: b, out: make(chan Event, 64), kick: make(chan struct{}, 1), done: make(chan struct{}),
		roots: map[string]*root{}, pending: map[string]error{}, seen: map[string]os.FileInfo{}}
	w.wg.Add(2)
	go func() {
		defer w.wg.Done()
		b.run(w.handle)
	}()
	go func() {
		defer w.wg.Done()
		w.deliver()
	}()
	return w
}

// Events are the paths that changed. It closes when the watcher does.
func (w *Watcher) Events() <-chan Event { return w.out }

// Add watches path: a file, or a folder and its entries. It needn't exist yet; it's watched once it does.
func (w *Watcher) Add(path string) error { return w.add(path, false, nil) }

// AddTree watches a folder and everything under it, but for what skip says to leave out. skip is given each path
// relative to root, with slashes ("node_modules", ".git/objects"); a folder it skips is left out with all it holds.
func (w *Watcher) AddTree(root string, skip func(rel string) bool) error {
	return w.add(root, true, skip)
}

func (w *Watcher) add(path string, tree bool, skip func(string) bool) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if skip == nil {
		skip = func(string) bool { return false }
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.roots[path]; ok {
		return nil
	}
	r := &root{path: path, tree: tree, skip: skip}
	w.roots[path] = r
	err = w.settle(r, false)
	if err != nil {
		delete(w.roots, path)
	}
	w.prune()
	return err
}

// Remove stops watching a path added with Add or AddTree.
func (w *Watcher) Remove(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	r, ok := w.roots[path]
	if !ok {
		return nil
	}
	w.stopStream(r)
	delete(w.roots, path)
	for p := range w.seen {
		if under(p, path) {
			delete(w.seen, p)
		}
	}
	w.prune()
	return nil
}

// Close stops the watcher and closes Events.
func (w *Watcher) Close() error {
	var err error
	w.once.Do(func() {
		w.mu.Lock()
		for _, r := range w.roots {
			w.stopStream(r)
		}
		w.mu.Unlock()
		close(w.done)
		err = w.b.close()
		w.wg.Wait()
		close(w.out)
	})
	return err
}

// settle finds r's anchor and watches what it needs there. announce queues what's there now: it arrived unseen.
func (w *Watcher) settle(r *root, announce bool) error {
	w.stopStream(r)
	var target string
	var statErr error
	for {
		var fi os.FileInfo
		fi, statErr = os.Stat(r.path)
		r.dir = statErr == nil && fi.IsDir()
		target = r.path
		if !r.dir && !r.tree {
			target = filepath.Dir(r.path)
		}
		r.anchor = existingDir(target)
		if r.anchor == "" {
			return &fs.PathError{Op: "watch", Path: r.path, Err: fs.ErrNotExist}
		}
		if err := w.b.watchDir(r.anchor); err != nil {
			return err
		}
		// A folder made before the watch began sends nothing: look again.
		if existingDir(target) == r.anchor {
			break
		}
	}
	if r.anchor != target {
		return nil
	}
	if announce {
		w.queue(r.path, nil)
	}
	switch {
	case r.tree && r.dir:
		if w.startStream(r) && !announce {
			return nil
		}
		return w.walk(r, r.path, announce)
	case r.dir:
		ents, _ := os.ReadDir(r.path)
		for _, e := range ents {
			p := filepath.Join(r.path, e.Name())
			if announce {
				w.queue(p, nil)
			}
			if !e.IsDir() {
				if err := w.watchFile(p); err != nil {
					return err
				}
			}
		}
	case statErr == nil:
		return w.watchFile(r.path)
	default:
		// Made since it was looked for, while its folder's watch began: that watch won't report it.
		if fi, err := os.Stat(r.path); err == nil && !fi.IsDir() {
			w.queue(r.path, nil)
			return w.watchFile(r.path)
		}
	}
	return nil
}

// walk watches dir and what's under it for tree root r.
func (w *Watcher) walk(r *root, dir string, announce bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			return nil
		}
		if p != r.path && r.skip(rel(r.path, p)) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if announce {
			w.queue(p, nil)
		}
		if r.stream != nil {
			return nil
		}
		if d.IsDir() {
			return w.b.watchDir(p)
		}
		if d.Type().IsRegular() {
			return w.watchFile(p)
		}
		return nil
	})
}

// existingDir is p if it's a folder, or its nearest parent that is.
func existingDir(p string) string {
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		up := filepath.Dir(p)
		if up == p {
			return ""
		}
		p = up
	}
}

func rel(root, p string) string {
	r, _ := filepath.Rel(root, p)
	return filepath.ToSlash(r)
}

func under(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+string(filepath.Separator))
}

// inTree is p inside tree root r and not skipped.
func inTree(r *root, p string) bool {
	if !under(p, r.path) {
		return false
	}
	if p == r.path {
		return true
	}
	parts := strings.Split(rel(r.path, p), "/")
	for i := range parts {
		if r.skip(strings.Join(parts[:i+1], "/")) {
			return false
		}
	}
	return true
}

// wantDir is a folder some root needs watched.
func (w *Watcher) wantDir(p string) bool {
	for _, r := range w.roots {
		if r.anchor == p || (r.walked() && inTree(r, p)) {
			return true
		}
	}
	return false
}

// wantFile is a file some root needs watched.
func (w *Watcher) wantFile(p string) bool {
	for _, r := range w.roots {
		switch {
		case r.tree:
			if r.walked() && inTree(r, p) {
				return true
			}
		case r.dir:
			if filepath.Dir(p) == r.path {
				return true
			}
		case p == r.path:
			return true
		}
	}
	return false
}

// relevant is a change some root was added for.
func (w *Watcher) relevant(p string) bool {
	for _, r := range w.roots {
		switch {
		case r.tree:
			if inTree(r, p) {
				return true
			}
		case p == r.path:
			return true
		case r.dir && filepath.Dir(p) == r.path:
			return true
		}
	}
	return false
}

// prune stops watching what no root needs.
func (w *Watcher) prune() {
	for _, p := range w.b.watched() {
		if !w.wantDir(p) && !w.wantFile(p) {
			w.b.unwatch(p)
		}
	}
}

// handle takes the OS's changes: keeps the watches up with what was created and removed, and queues what to send.
func (w *Watcher) handle(evs []raw) {
	w.mu.Lock()
	defer w.mu.Unlock()
	moved := false
	for _, e := range evs {
		switch e.op {
		case opOverflow:
			moved = true
			for p := range w.roots {
				w.queue(p, nil)
			}
			continue
		case opChanged:
			if !e.dir {
				w.unchanged(e.path)
			}
		case opAttrib:
			if w.unchanged(e.path) {
				continue
			}
		case opRemoved:
			moved = true
			delete(w.seen, e.path)
			w.b.unwatchUnder(e.path)
			// A file replaced by a rename is still there under a new inode.
			if fi, err := os.Lstat(e.path); err == nil && fi.Mode().IsRegular() && w.wantFile(e.path) {
				_ = w.watchFile(e.path)
			}
		case opCreated:
			if e.dir {
				moved = true
				for _, r := range w.roots {
					if r.walked() && e.path != r.path && inTree(r, e.path) {
						if err := w.walk(r, e.path, true); err != nil {
							w.queue(r.path, err)
						}
					}
				}
			} else if w.wantFile(e.path) {
				_ = w.watchFile(e.path)
			}
		}
		if w.relevant(e.path) {
			w.queue(e.path, nil)
		}
	}
	if moved {
		w.resettle()
	}
}

// startStream watches tree root r with the backend's one handle, if it has one; w.mu is held.
func (w *Watcher) startStream(r *root) bool {
	tb, ok := w.b.(treeBackend)
	if !ok {
		return false
	}
	real, err := filepath.EvalSymlinks(r.path)
	if err != nil {
		real = r.path
	}
	s := &stream{real: real, since: time.Now()}
	stop, err := tb.watchTree(real, excludes(r.path, real, r.skip, maxExclude), func(evs []fsevent) {
		w.handleStream(r, s, evs)
	})
	if err != nil {
		return false
	}
	s.stop = stop
	r.stream = s
	return true
}

// stopStream stops r's stream, if it has one; w.mu is held.
func (w *Watcher) stopStream(r *root) {
	if r.stream == nil {
		return
	}
	r.stream.stopped = true
	r.stream.stop()
	r.stream = nil
}

// handleStream takes the changes a tree's stream saw. The root folder's own watch tells when it goes.
func (w *Watcher) handleStream(r *root, s *stream, evs []fsevent) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s.stopped {
		return
	}
	for _, e := range evs {
		if e.flags&(evUserDropped|evKernelDropped) != 0 {
			w.queue(r.path, nil)
			continue
		}
		p, ok := treePath(s.real, r.path, e.path)
		if !ok || !inTree(r, p) {
			continue
		}
		if w.before(s, p, e.flags) {
			continue
		}
		if e.flags&evItemIsFile != 0 {
			same := w.unchanged(p)
			if same && e.flags&(evItemCreated|evItemRemoved|evItemRenamed|evItemModified) == 0 {
				continue
			}
		}
		w.queue(p, nil)
		// A folder moved in sends nothing for what it holds.
		if p != r.path && e.flags&evItemIsDir != 0 && e.flags&(evItemCreated|evItemRenamed) != 0 {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				_ = w.walk(r, p, true)
			}
		}
	}
}

// before says event e for p tells of what happened before stream s began: a file not changed since, or a folder
// made before that hasn't moved. What a folder holds tells of its own changes. w.mu is held.
func (w *Watcher) before(s *stream, p string, flags uint32) bool {
	fi, err := os.Lstat(p)
	switch {
	case err != nil:
		return false
	case flags&evItemIsDir != 0:
		return flags&evItemRenamed == 0 && madeAt(fi).Before(s.since)
	case changedAt(fi).Before(s.since):
		w.seen[p] = fi
		return true
	}
	return false
}

// watchFile watches file p, keeping how it looks now so a metadata-only event on it is told apart; w.mu is held.
func (w *Watcher) watchFile(p string) error {
	if fi, err := os.Lstat(p); err == nil {
		w.seen[p] = fi
	}
	return w.b.watchFile(p)
}

// unchanged says file p looks as it did at its last event, and keeps how it looks now; w.mu is held.
func (w *Watcher) unchanged(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil {
		delete(w.seen, p)
		return false
	}
	old, ok := w.seen[p]
	w.seen[p] = fi
	return ok && os.SameFile(old, fi) && old.Size() == fi.Size() && old.ModTime().Equal(fi.ModTime()) && old.Mode() == fi.Mode()
}

// resettle moves each root's anchor to where it is now, after folders came and went.
func (w *Watcher) resettle() {
	changed := false
	for _, r := range w.roots {
		was, wasDir := r.anchor, r.dir
		fi, err := os.Stat(r.path)
		isDir := err == nil && fi.IsDir()
		target := r.path
		if !isDir && !r.tree {
			target = filepath.Dir(r.path)
		}
		if existingDir(target) == was && isDir == wasDir {
			continue
		}
		changed = true
		if isDir != wasDir {
			w.queue(r.path, nil)
		}
		if err := w.settle(r, true); err != nil {
			w.queue(r.path, err)
		}
	}
	if changed {
		w.prune()
	}
}

// queue holds a path to send; w.mu is held.
func (w *Watcher) queue(p string, err error) {
	if old, ok := w.pending[p]; !ok || old == nil {
		w.pending[p] = err
	}
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// deliver sends what's queued, Delay after the first change of a burst.
func (w *Watcher) deliver() {
	for {
		select {
		case <-w.done:
			return
		case <-w.kick:
		}
		t := time.NewTimer(Delay)
		select {
		case <-w.done:
			t.Stop()
			return
		case <-t.C:
		}
		w.mu.Lock()
		got := w.pending
		w.pending = map[string]error{}
		w.mu.Unlock()
		for _, p := range slices.Sorted(maps.Keys(got)) {
			select {
			case w.out <- Event{Path: p, Err: got[p]}:
			case <-w.done:
				return
			}
		}
	}
}
