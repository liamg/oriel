package fswatch

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/isolate"
)

func TestTreePath(t *testing.T) {
	for _, c := range []struct {
		real, p, want string
		ok            bool
	}{
		{"/private/var/x", "/private/var/x", "/var/x", true},
		{"/private/var/x", "/private/var/x/", "/var/x", true},
		{"/private/var/x", "/private/var/x/src/a.go", "/var/x/src/a.go", true},
		{"/private/var/x", "/private/var/x/src/", "/var/x/src", true},
		{"/private/var/x", "/private/var/xy/a.go", "", false},
		{"/private/var/x", "/private/var", "", false},
		{"/private/var/x", "/Private/Var/X/Src/a.go", "/var/x/Src/a.go", true},
		{"/", "/etc/hosts", "/var/x/etc/hosts", true},
	} {
		got, ok := treePath(c.real, "/var/x", c.p)
		if got != c.want || ok != c.ok {
			t.Errorf("treePath(%q, %q) = %q, %v; want %q, %v", c.real, c.p, got, ok, c.want, c.ok)
		}
	}
}

func TestExcludes(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"node_modules/a/node_modules", "src/node_modules", "src/x", ".git/objects", "a/b/c/d/node_modules"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	skip := func(rel string) bool { return rel == ".git/objects" || filepath.Base(rel) == "node_modules" }
	got := excludes(root, "/real", skip, maxExclude)
	slices.Sort(got)
	want := []string{"/real/.git/objects", "/real/a/b/c/d/node_modules", "/real/node_modules", "/real/src/node_modules"}
	if !slices.Equal(got, want) {
		t.Fatalf("excludes = %v, want %v", got, want)
	}
	if got := excludes(root, "/real", skip, 2); len(got) != 2 || !slices.Contains(got, "/real/node_modules") {
		t.Fatalf("excludes with max 2 = %v, want 2 holding the shallowest", got)
	}
}

// treeFake is the OS's watcher with a treeBackend whose events the test sends.
type treeFake struct {
	backend
	mu      sync.Mutex
	root    string
	exclude []string
	send    func([]fsevent)
	starts  int
	stops   int
}

func (f *treeFake) watchTree(root string, exclude []string, send func([]fsevent)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.root, f.exclude, f.send = root, exclude, send
	f.starts++
	return func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.stops++
	}, nil
}

func (f *treeFake) emit(evs ...fsevent) {
	f.mu.Lock()
	send := f.send
	f.mu.Unlock()
	send(evs)
}

func TestTreeStream(t *testing.T) {
	real := t.TempDir()
	write(t, filepath.Join(real, "src", "main.go"), "package main\n")
	write(t, filepath.Join(real, "node_modules", "x", "y.js"), "x")
	write(t, filepath.Join(real, ".git", "objects", "ab", "cd"), "x")
	root := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, root); err != nil {
		t.Fatal(err)
	}
	b, err := newBackend()
	if err != nil {
		t.Fatal(err)
	}
	f := &treeFake{backend: b}
	c := clocktest.New(time.Unix(0, 0))
	w := newWatcher(f, c)
	t.Cleanup(func() { w.Close() })
	skip := func(rel string) bool { return rel == ".git/objects" || filepath.Base(rel) == "node_modules" }
	if err := w.AddTree(root, skip); err != nil {
		t.Fatal(err)
	}

	if f.root != real {
		t.Fatalf("stream on %q, want the resolved %q", f.root, real)
	}
	if want := []string{filepath.Join(real, ".git", "objects"), filepath.Join(real, "node_modules")}; !slices.Equal(slices.Sorted(slices.Values(f.exclude)), want) {
		t.Fatalf("excluded %v, want %v", f.exclude, want)
	}
	if got := b.watched(); !slices.Equal(got, []string{root}) {
		t.Fatalf("OS watches %v, want only the root", got)
	}

	f.emit(
		fsevent{filepath.Join(real, "node_modules", "x", "y.js"), evItemModified | evItemIsFile},
		fsevent{filepath.Join(real, ".git", "objects", "ef", "01"), evItemCreated | evItemIsFile},
		fsevent{"/elsewhere/a.go", evItemModified | evItemIsFile},
	)
	if got := queued(w); len(got) != 0 {
		t.Fatalf("events from skipped or outside paths: %v", got)
	}

	write(t, filepath.Join(real, "src", "main.go"), "// more\n")
	f.emit(fsevent{filepath.Join(real, "src", "main.go"), evItemModified | evItemIsFile})
	c.Advance(Delay)
	expect(t, w, filepath.Join(root, "src", "main.go"))

	f.emit(fsevent{"", evKernelDropped})
	c.Advance(Delay)
	expect(t, w, root)

	// A folder moved in arrives whole: what's in it is announced too.
	write(t, filepath.Join(real, "moved", "deep", "new.txt"), "x")
	f.emit(fsevent{filepath.Join(real, "moved"), evItemRenamed | evItemIsDir})
	c.Advance(Delay)
	expect(t, w, filepath.Join(root, "moved", "deep", "new.txt"))
	if got := b.watched(); !slices.Equal(got, []string{root}) {
		t.Fatalf("OS watches %v after a folder arrived, want only the root", got)
	}

	if err := w.Remove(root); err != nil {
		t.Fatal(err)
	}
	if f.stops != 1 {
		t.Fatalf("stream stopped %d times on Remove, want 1", f.stops)
	}
	f.emit(fsevent{filepath.Join(real, "src", "main.go"), evItemModified | evItemIsFile})
	if slices.Contains(queued(w), filepath.Join(root, "src", "main.go")) {
		t.Fatal("an event after Remove")
	}
}

// rawScript is a backend whose OS events are only the ones a test hands it, taken at once on the test's goroutine:
// what the Watcher does with an event, not when the OS sends it.
type rawScript struct {
	scripted
	mu     sync.Mutex
	handle func([]raw)
	ready  chan struct{}
}

func newRawScript() *rawScript {
	return &rawScript{scripted: scripted{done: make(chan struct{})}, ready: make(chan struct{})}
}

func (b *rawScript) run(handle func([]raw)) {
	b.mu.Lock()
	b.handle = handle
	b.mu.Unlock()
	close(b.ready)
	<-b.done
}

// os hands the Watcher evs as the OS would, once it's listening.
func (b *rawScript) os(evs ...raw) {
	<-b.ready
	b.mu.Lock()
	h := b.handle
	b.mu.Unlock()
	h(evs)
}

// A tree whose root goes stops its stream, says the root changed, and starts a stream again once the root is back,
// with what it holds.
func TestTreeStreamRootRemoved(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	write(t, filepath.Join(root, "a.txt"), "x")
	b := newRawScript()
	f := &treeFake{backend: b}
	c := clocktest.New(time.Unix(0, 0))
	w := newWatcher(f, c)
	t.Cleanup(func() { w.Close() })
	if err := w.AddTree(root, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	b.os(raw{path: root, dir: true, op: opRemoved}) // the root's own watch: IN_DELETE_SELF, or kqueue's NOTE_DELETE
	f.mu.Lock()
	stops := f.stops
	f.mu.Unlock()
	if stops != 1 {
		t.Fatalf("stream stopped %d times after its root went, want 1", stops)
	}
	c.Advance(Delay)
	expect(t, w, root)

	tmp := filepath.Join(parent, "tmp")
	write(t, filepath.Join(tmp, "b.txt"), "x")
	if err := os.Rename(tmp, root); err != nil {
		t.Fatal(err)
	}
	b.os(raw{path: root, dir: true, op: opCreated}) // the parent's watch, which the Watcher fell back to
	f.mu.Lock()
	again, stops := f.root, f.stops
	f.mu.Unlock()
	if again != root || stops != 1 {
		t.Fatalf("once the root came back: stream on %q, stopped %d times; want a new one on %q, stopped once", again, stops, root)
	}
	c.Advance(Delay)
	expect(t, w, filepath.Join(root, "b.txt"))
}

// A tree's root made as the watcher closes (a config's lua/ written just before the app quits) starts no stream: the
// OS's event for it can be taken in once Close has stopped the streams it knew.
func TestTreeRootMadeAsTheWatcherClosesStartsNoStream(t *testing.T) {
	isolate.Test(t)
	root := filepath.Join(t.TempDir(), "lua")
	b := newRawScript()
	f := &treeFake{backend: b}
	w := newWatcher(f, clock.Real())
	if err := w.AddTree(root, nil); err != nil {
		t.Fatal(err)
	}
	w.Close()
	write(t, filepath.Join(root, "my", "tickets.lua"), "return {}\n")
	b.os(raw{path: root, dir: true, op: opCreated}) // the parent's watch, which the tree waits on until its root is made
	f.mu.Lock()
	starts, stops := f.starts, f.stops
	f.mu.Unlock()
	if starts != stops {
		t.Fatalf("%d streams started, %d stopped: one outlives the watcher", starts, stops)
	}
}
