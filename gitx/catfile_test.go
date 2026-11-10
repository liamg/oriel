package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/liamg/oriel/gitx/gittest"
)

// FileAt a commit's hash reads every file through one git: what it reads is what git cat-file blob gives, a file
// the commit lacks is not there, and a path with spaces reads as any other.
// ref: oracle git
func TestFileAtReadsThroughOneGit(t *testing.T) {
	r := gittest.New(t)
	files := map[string]string{"a b.txt": "spaced\n", "empty.txt": ""}
	for i := range 30 {
		files[fmt.Sprintf("d/f%02d.txt", i)] = strings.Repeat(fmt.Sprintf("line of %d\n", i), i)
	}
	r.Write(files)
	r.Commit("Ada", "base")
	hash := strings.TrimSpace(r.Git("rev-parse", "--short", "HEAD"))
	runs := gittest.CountGit(t)
	repo := Repo{Dir: r.Dir, Cache: testCache(t)}
	for p := range files {
		got, ok := repo.FileAt(context.Background(), hash, p)
		if want := r.Git("cat-file", "blob", hash+":"+p); !ok || string(got) != want {
			t.Errorf("%s: %q %v, want %q", p, got, ok, want)
		}
	}
	if _, ok := repo.FileAt(context.Background(), hash, "nope.txt"); ok {
		t.Error("a file the commit lacks was there")
	}
	if got, ok := repo.FileAt(context.Background(), hash, "a b.txt"); !ok || string(got) != "spaced\n" {
		t.Errorf("after a missing file: %q %v", got, ok)
	}
	if n := runs() - len(files); n > 1 {
		t.Errorf("%d files read with %d gits", len(files)+2, n)
	}
}

// A git that never answers holds up neither the call whose ctx ends nor one waiting behind it.
func TestObjectStopsWithCtx(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell script as git")
	}
	bin := t.TempDir()
	started := filepath.Join(bin, "started")
	if err := syscall.Mkfifo(started, 0o600); err != nil {
		t.Fatal(err)
	}
	// It says it has started (a write to the fifo waits for the test to read it), then never answers.
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho > '"+started+"'\nexec sleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	repo := Repo{Dir: t.TempDir(), Cache: testCache(t)}
	ask := func(ctx context.Context) chan error {
		ch := make(chan error, 1)
		go func() {
			_, _, err := repo.object(ctx, "0123456789:a.txt")
			ch <- err
		}()
		return ch
	}
	firstCtx, stopFirst := context.WithCancel(context.Background())
	defer stopFirst()
	first := ask(firstCtx)
	if _, err := os.ReadFile(started); err != nil { // the first read's git is running, and holds the batch
		t.Fatal(err)
	}
	secondCtx, stopSecond := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopSecond()
	second := ask(secondCtx)
	select {
	case err := <-second:
		if err == nil {
			t.Error("a read behind a hung git had no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read waiting behind a hung git outlived its ctx")
	}
	stopFirst()
	select {
	case err := <-first:
		if err == nil {
			t.Error("a read from a hung git had no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a read from a hung git outlived its ctx")
	}
}

// testCache is a Cache closed when the test ends.
func testCache(t *testing.T) *Cache {
	c := NewCache()
	t.Cleanup(c.Close)
	return c
}

// Closing the cache stops its cat-file, and a Repo on it reads without one after.
func TestCacheCloseStopsItsGit(t *testing.T) {
	r := gittest.New(t)
	r.Write(map[string]string{"a.txt": "a\n"})
	r.Commit("Ada", "base")
	hash := strings.TrimSpace(r.Git("rev-parse", "HEAD"))
	c := NewCache()
	repo := Repo{Dir: r.Dir, Cache: c}
	if got, ok := repo.FileAt(context.Background(), hash, "a.txt"); !ok || string(got) != "a\n" {
		t.Fatalf("%q %v", got, ok)
	}
	b := c.batch(r.Dir + "\x00")
	proc := b.cmd.Process
	c.Close()
	if b.cmd != nil || proc.Signal(syscall.Signal(0)) == nil {
		t.Error("the cat-file still runs after Close")
	}
	if got, ok := repo.FileAt(context.Background(), hash, "a.txt"); !ok || string(got) != "a\n" {
		t.Errorf("after Close: %q %v", got, ok)
	}
}
