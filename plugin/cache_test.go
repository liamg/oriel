package plugin

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/ui"
)

// gate is a fetch that waits for each key's answer, counting calls and cancels.
type gate struct {
	mu       sync.Mutex
	answers  map[string]chan string
	calls    map[string]int
	canceled map[string]int
	counted  func() // a call or a cancel was counted
}

func newGate() *gate {
	return &gate{answers: map[string]chan string{}, calls: map[string]int{}, canceled: map[string]int{}, counted: func() {}}
}

func (g *gate) ch(k string) chan string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.answers[k] == nil {
		g.answers[k] = make(chan string, 8)
	}
	return g.answers[k]
}

func (g *gate) fetch(ctx context.Context, k string) (string, error) {
	g.mu.Lock()
	g.calls[k]++
	g.mu.Unlock()
	g.counted()
	select {
	case v := <-g.ch(k):
		if v == "" {
			return "", errors.New("gone")
		}
		return v, nil
	case <-ctx.Done():
		g.mu.Lock()
		g.canceled[k]++
		g.mu.Unlock()
		g.counted()
		return "", ctx.Err()
	}
}

func (g *gate) count(m map[string]int, k string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return m[k]
}

func (g *gate) answer(k, v string) { g.ch(k) <- v }

type frames struct{ n atomic.Int64 }

func (f *frames) next()    { f.n.Add(1) }
func (f *frames) get() int { return int(f.n.Load()) }
func cacheFor(g *gate, o CacheOpts[string, string]) (*Cache[string, string], *loop, *frames) {
	lp, fr := newLoop(), &frames{}
	g.counted = lp.Redraw // the loop looks again when a fetch begins or ends
	if o.Frame == nil {
		o.Frame = fr.get
	}
	if o.Watch == nil {
		o.Watch = func() (*fswatch.Watcher, error) { return nil, fswatch.ErrUnsupported }
	}
	return NewCache(lp, g.fetch, o), lp, fr
}

func TestCacheMissFetchesOffTheLoopAndShares(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{})
	v, s := c.Get("a")
	if v != "" || s.Phase != ui.Pending || s.Stale {
		t.Fatalf("a miss is %q %+v, want pending", v, s)
	}
	c.Get("a")
	lp.until(t, func() bool { return g.count(g.calls, "a") == 1 })
	g.answer("a", "A")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready })
	if v, _ := c.Get("a"); v != "A" {
		t.Fatalf("got %q", v)
	}
	if n := g.count(g.calls, "a"); n != 1 {
		t.Fatalf("fetched %d times, want once", n)
	}
}

// Fetching names the keys whose fetch is under way: what a wait that never ends was waiting for.
func TestCacheSaysWhatItsFetching(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{})
	c.Get("a")
	c.Get("b")
	if got := c.Fetching(); len(got) != 2 {
		t.Fatalf("fetching %q, want a and b", got)
	}
	g.answer("a", "A")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready })
	if got := c.Fetching(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("fetching %q, want b", got)
	}
	g.answer("b", "B")
	lp.until(t, func() bool { _, s := c.Get("b"); return s.Phase == ui.Ready })
	if got := c.Fetching(); len(got) != 0 {
		t.Fatalf("fetching %q after both landed", got)
	}
}

func TestCacheServesStaleWhileItRevalidates(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{})
	g.answer("a", "A1")
	c.Get("a")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready })
	c.Invalidate("a")
	v, s := c.Get("a")
	if v != "A1" || s.Phase != ui.Pending || !s.Stale {
		t.Fatalf("revalidating shows %q %+v, want the old value, stale", v, s)
	}
	g.answer("a", "A2")
	lp.until(t, func() bool { v, s := c.Get("a"); return s.Phase == ui.Ready && v == "A2" })
}

func TestCacheFailureKeepsTheValueAndRetriesOnInvalidate(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{})
	c.Get("a")
	g.answer("a", "")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Failed })
	if _, s := c.Get("a"); s.Err == nil || g.count(g.calls, "a") != 1 {
		t.Fatalf("a failure is %+v after %d fetches; want its error, and no retry until asked", s, g.count(g.calls, "a"))
	}
	c.Invalidate("a")
	g.answer("a", "A")
	lp.until(t, func() bool { v, s := c.Get("a"); return v == "A" && s.Phase == ui.Ready && s.Err == nil })
	c.Invalidate("a")
	g.answer("a", "")
	c.Get("a")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready && s.Err != nil })
	if v, _ := c.Get("a"); v != "A" {
		t.Fatalf("a failed revalidation dropped the value: %q", v)
	}
}

func TestCacheCancelsWhatAFrameDidNotAskFor(t *testing.T) {
	g := newGate()
	c, lp, fr := cacheFor(g, CacheOpts[string, string]{})
	c.Get("a")
	c.Get("b")
	lp.until(t, func() bool { return g.count(g.calls, "a") == 1 && g.count(g.calls, "b") == 1 })
	fr.next()
	c.Get("a") // b isn't asked for in this frame, so the next one cancels it
	fr.next()
	c.Get("a")
	lp.until(t, func() bool { return g.count(g.canceled, "b") == 1 })
	if g.count(g.canceled, "a") != 0 {
		t.Fatal("a was asked for, yet cancelled")
	}
	fr.next()
	if _, s := c.Get("b"); s.Phase != ui.Pending {
		t.Fatalf("b asked for again is %+v, want pending", s)
	}
	lp.until(t, func() bool { return g.count(g.calls, "b") == 2 })
	g.answer("b", "B")
	lp.until(t, func() bool { v, _ := c.Get("b"); return v == "B" })
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{MaxEntries: 2})
	for _, k := range []string{"a", "b"} {
		g.answer(k, k)
		lp.until(t, func() bool { _, s := c.Get(k); return s.Phase == ui.Ready })
	}
	c.Get("a") // b is now the least recent
	g.answer("c", "c")
	lp.until(t, func() bool { _, s := c.Get("c"); return s.Phase == ui.Ready })
	if _, s := c.Get("a"); s.Phase != ui.Ready {
		t.Fatalf("a, used recently, was dropped: %+v", s)
	}
	if _, s := c.Get("b"); s.Phase != ui.Pending || s.Stale {
		t.Fatalf("b should have been dropped and fetched again: %+v", s)
	}
	lp.until(t, func() bool { return g.count(g.calls, "b") == 2 })
}

func TestCacheBoundsBytes(t *testing.T) {
	g := newGate()
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{MaxBytes: 10, Size: func(v string) int64 { return int64(len(v)) }})
	g.answer("a", "123456")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready })
	g.answer("b", "123456")
	lp.until(t, func() bool { _, s := c.Get("b"); return s.Phase == ui.Ready })
	if c.Bytes() > 10 {
		t.Fatalf("holds %d bytes, over 10", c.Bytes())
	}
	if _, s := c.Get("a"); s.Phase != ui.Pending {
		t.Fatalf("a should have made room for b: %+v", s)
	}
}

func TestCacheRefetchesAfterMaxAge(t *testing.T) {
	g := newGate()
	now := time.Unix(1000, 0)
	c, lp, _ := cacheFor(g, CacheOpts[string, string]{MaxAge: time.Minute, Now: func() time.Time { return now }})
	g.answer("a", "A1")
	lp.until(t, func() bool { _, s := c.Get("a"); return s.Phase == ui.Ready })
	now = now.Add(30 * time.Second)
	if _, s := c.Get("a"); s.Phase != ui.Ready || g.count(g.calls, "a") != 1 {
		t.Fatalf("a fresh value was fetched again: %+v", s)
	}
	now = now.Add(31 * time.Second)
	if v, s := c.Get("a"); v != "A1" || !s.Stale {
		t.Fatalf("an old value shows %q %+v, want it stale while it's fetched", v, s)
	}
	g.answer("a", "A2")
	lp.until(t, func() bool { v, _ := c.Get("a"); return v == "A2" })
}

func TestCacheWatchesPaths(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	lp, fr := newLoop(), &frames{}
	var script *fswatch.Script
	c := NewCache(lp, func(ctx context.Context, k string) (string, error) {
		b, err := os.ReadFile(k)
		return string(b), err
	}, CacheOpts[string, string]{Path: func(k string) string { return k }, Frame: fr.get, Watch: func() (*fswatch.Watcher, error) {
		w, s := fswatch.NewScripted()
		script = s
		return w, nil
	}})
	defer c.Close()
	lp.until(t, func() bool { v, _ := c.Get(p); return v == "one" })
	if v, _ := c.Get(p); v != "one" {
		t.Fatalf("an unchanged file was read again: %q", v)
	}
	if err := os.WriteFile(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	changed := make(chan struct{})
	go func() {
		defer close(changed)
		script.Change(p) // returns once the loop has taken it in
	}()
	lp.until(t, func() bool { v, _ := c.Get(p); return v == "two" })
	<-changed
}

func TestCacheGetNeverBlocks(t *testing.T) {
	g := newGate()
	c, _, _ := cacheFor(g, CacheOpts[string, string]{})
	done := make(chan struct{})
	go func() {
		for i := range 100 {
			c.Get(string(rune('a' + i%26)))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Get waited on a fetch")
	}
}

func TestFilesAndDirsCaches(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(p, []byte("one\r\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	lp := newLoop()
	files := NewFiles(lp, CacheOpts[string, File]{Frame: func() int { return 0 }})
	defer files.Close()
	lp.until(t, func() bool { _, s := files.Get(p); return s.Phase == ui.Ready })
	if f, _ := files.Get(p); strings.Join(f.Lines, "|") != "one|two" {
		t.Fatalf("lines %q", f.Lines)
	}
	lp.until(t, func() bool { _, s := files.Get(filepath.Join(dir, "none")); return s.Phase == ui.Failed })
	if _, s := files.Get(filepath.Join(dir, "none")); !errors.Is(s.Err, fs.ErrNotExist) {
		t.Fatalf("a missing file fails with %v", s.Err)
	}
	dirs := NewDirs(lp, CacheOpts[string, []fs.DirEntry]{Frame: func() int { return 0 }})
	defer dirs.Close()
	lp.until(t, func() bool { _, s := dirs.Get(dir); return s.Phase == ui.Ready })
	if es, _ := dirs.Get(dir); len(es) != 1 || es[0].Name() != "f.txt" {
		t.Fatalf("entries %v", es)
	}
}
