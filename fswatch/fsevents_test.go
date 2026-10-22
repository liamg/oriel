package fswatch

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"
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
	stops   int
}

func (f *treeFake) watchTree(root string, exclude []string, send func([]fsevent)) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.root, f.exclude, f.send = root, exclude, send
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
	w := newWatcher(f)
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
	if got := quiet(w, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("events from skipped or outside paths: %v", got)
	}

	f.emit(fsevent{filepath.Join(real, "src", "main.go"), evItemModified | evItemIsFile})
	expect(t, w, filepath.Join(root, "src", "main.go"))

	f.emit(fsevent{"", evKernelDropped})
	expect(t, w, root)

	// A folder moved in arrives whole: what's in it is announced too.
	write(t, filepath.Join(real, "moved", "deep", "new.txt"), "x")
	quiet(w, 200*time.Millisecond)
	f.emit(fsevent{filepath.Join(real, "moved"), evItemRenamed | evItemIsDir})
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
	if got := quiet(w, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("events after Remove: %v", got)
	}
}

func TestTreeStreamRootRemoved(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	write(t, filepath.Join(root, "a.txt"), "x")
	b, err := newBackend()
	if err != nil {
		t.Fatal(err)
	}
	f := &treeFake{backend: b}
	w := newWatcher(f)
	t.Cleanup(func() { w.Close() })
	if err := w.AddTree(root, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	expect(t, w, root)
	quiet(w, 200*time.Millisecond)
	f.mu.Lock()
	stops := f.stops
	f.mu.Unlock()
	if stops != 1 {
		t.Fatalf("stream stopped %d times after its root went, want 1", stops)
	}

	tmp := filepath.Join(parent, "tmp")
	write(t, filepath.Join(tmp, "b.txt"), "x")
	if err := os.Rename(tmp, root); err != nil {
		t.Fatal(err)
	}
	expect(t, w, filepath.Join(root, "b.txt"))
	f.mu.Lock()
	again := f.root
	f.mu.Unlock()
	if again != root {
		t.Fatalf("no new stream once the root came back: %q", again)
	}
}
