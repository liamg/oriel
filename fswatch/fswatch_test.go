package fswatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func start(t *testing.T) *Watcher {
	t.Helper()
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// expect waits for an event for p.
func expect(t *testing.T, w *Watcher, p string) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-w.Events():
			if ev.Path == p {
				return
			}
		case <-deadline:
			t.Fatalf("no event for %s", p)
		}
	}
}

// quiet collects what arrives over d.
func quiet(w *Watcher, d time.Duration) []string {
	var got []string
	deadline := time.After(d)
	for {
		select {
		case ev := <-w.Events():
			got = append(got, ev.Path)
		case <-deadline:
			return got
		}
	}
}

func TestFileWrite(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "log.jsonl")
	write(t, f, "a\n")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	write(t, f, "b\n")
	expect(t, w, f)
}

func TestFileSiblingsAreQuiet(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "mine.jsonl")
	write(t, f, "a\n")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "other.jsonl"), "x\n")
	if got := quiet(w, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("events for a sibling: %v", got)
	}
}

func TestBurstIsCoalesced(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "log.jsonl")
	write(t, f, "")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	for range 200 {
		write(t, f, "line\n")
	}
	got := quiet(w, 500*time.Millisecond)
	if len(got) == 0 || len(got) > 3 {
		t.Fatalf("200 writes gave %d events, want 1 to 3", len(got))
	}
}

func TestPathThatDoesNotExistYet(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a", "b", "log.jsonl")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	write(t, f, "a\n")
	expect(t, w, f)
	write(t, f, "b\n")
	expect(t, w, f)
}

func TestFolderEntries(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subagents")
	w := start(t)
	if err := w.Add(sub); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(sub, "agent-1.jsonl")
	write(t, a, "a\n")
	expect(t, w, a)
	write(t, a, "b\n")
	expect(t, w, a)
}

func TestReplacedByRename(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "index")
	write(t, f, "a")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		tmp := filepath.Join(dir, "index.lock")
		write(t, tmp, strings.Repeat("b", i+2))
		if err := os.Rename(tmp, f); err != nil {
			t.Fatal(err)
		}
		expect(t, w, f)
	}
}

func TestTree(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	write(t, filepath.Join(root, ".git", "objects", "ab", "cd"), "x")
	write(t, filepath.Join(root, "node_modules", "x", "y.js"), "x")
	write(t, filepath.Join(root, "src", "main.go"), "package main\n")
	skip := func(rel string) bool {
		return rel == ".git/objects" || filepath.Base(rel) == "node_modules"
	}
	w := start(t)
	if err := w.AddTree(root, skip); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(root, "node_modules", "x", "y.js"), "more")
	write(t, filepath.Join(root, ".git", "objects", "ab", "cd"), "more")
	write(t, filepath.Join(root, ".git", "objects", "ef", "01"), "new")
	if got := quiet(w, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("events from skipped folders: %v", got)
	}

	write(t, filepath.Join(root, "src", "main.go"), "// more\n")
	expect(t, w, filepath.Join(root, "src", "main.go"))
	write(t, filepath.Join(root, ".git", "HEAD"), "x")
	expect(t, w, filepath.Join(root, ".git", "HEAD"))

	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	quiet(w, 200*time.Millisecond)
	write(t, filepath.Join(deep, "new.txt"), "x")
	expect(t, w, filepath.Join(deep, "new.txt"))
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "log.jsonl")
	write(t, f, "a\n")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	if err := w.Remove(f); err != nil {
		t.Fatal(err)
	}
	if n := len(w.b.watched()); n != 0 {
		t.Fatalf("%d watches left after Remove", n)
	}
	write(t, f, "b\n")
	if got := quiet(w, 300*time.Millisecond); len(got) != 0 {
		t.Fatalf("events after Remove: %v", got)
	}
}

func TestIdleSendsNothing(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "log.jsonl")
	write(t, f, "a\n")
	w := start(t)
	if err := w.Add(f); err != nil {
		t.Fatal(err)
	}
	if got := quiet(w, 500*time.Millisecond); len(got) != 0 {
		t.Fatalf("events with nothing written: %v", got)
	}
}

func TestCloseClosesEvents(t *testing.T) {
	w := start(t)
	if err := w.Add(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-w.Events():
		if ok {
			t.Fatal("an event after Close")
		}
	case <-time.After(time.Second):
		t.Fatal("Events still open after Close")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
