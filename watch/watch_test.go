package watch

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/internal/leaktest"
)

var quick = Timing{Settle: 20 * time.Millisecond, Most: 200 * time.Millisecond, Grace: 300 * time.Millisecond, Fallback: 100 * time.Millisecond}

func manager(t *testing.T, o Options) *Manager {
	t.Helper()
	leaktest.Check(t)
	if o.Timing == (Timing{}) {
		o.Timing = quick
	}
	m := New(o)
	t.Cleanup(m.Close)
	return m
}

// counted is a source whose reading is how many times it's been read.
func counted(key string, poll bool, files ...Files) (Source[int], *atomic.Int32) {
	var n atomic.Int32
	return Source[int]{Key: key, Poll: poll, Files: files, Read: func(Reader) (int, error) { return int(n.Add(1)), nil }}, &n
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A polled source is read at its subscriptions' tightest MaxAge, shared between them, and not at all once none is left.
func TestPollsAtTheTightestNeedAndStopsWithNoSubscribers(t *testing.T) {
	m := manager(t, Options{})
	src, reads := counted("p", true)
	a := Subscribe(m, src, Need{MaxAge: time.Hour}, "a")
	eventually(t, "the first read", func() bool { _, _, ok := a.Get(); return ok })
	time.Sleep(150 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times at an hour's need, want 1", n)
	}
	b := Subscribe(m, src, Need{MaxAge: 50 * time.Millisecond}, "b")
	time.Sleep(300 * time.Millisecond)
	if n := reads.Load(); n < 3 || n > 9 {
		t.Fatalf("read %d times in 300ms at 50ms, want about 6", n)
	}
	if va, _, _ := a.Get(); va == 0 {
		t.Error("a doesn't share the reading")
	}
	a.Close()
	b.Close()
	time.Sleep(50 * time.Millisecond)
	before := reads.Load()
	time.Sleep(300 * time.Millisecond)
	if n := reads.Load() - before; n > 0 {
		t.Errorf("read %d times with no subscribers", n)
	}
}

// A watched source is read when its files change, once for a burst, and not for what it skips or while nothing changes.
func TestReadsAWatchedSourceWhenItsFilesChange(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "a.txt"), "1")
	m := manager(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true, Skip: func(rel string) bool { return rel == "skip" }})
	s := Subscribe(m, src, Need{}, "s")
	eventually(t, "the first read", func() bool { return reads.Load() == 1 })
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times with nothing changed", n)
	}
	write(t, filepath.Join(dir, "skip", "x"), "1")
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times for a skipped folder", n)
	}
	for i := range 5 {
		write(t, filepath.Join(dir, "a.txt"), string(rune('a'+i)))
	}
	eventually(t, "a read for the change", func() bool { return reads.Load() == 2 })
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 2 {
		t.Errorf("read %d times for one burst, want 2 in all", n)
	}
	select {
	case <-s.Changed():
	default:
		t.Error("Changed didn't fire")
	}
}

// Behind another app a Pause subscription reads nothing, and once back the stale source is read once; Keep goes on.
func TestPausesInTheBackgroundAndCatchesUpOnce(t *testing.T) {
	dir := t.TempDir()
	m := manager(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true})
	Subscribe(m, src, Need{}, "s")
	eventually(t, "the first read", func() bool { return reads.Load() == 1 })
	m.SetFocused(false)
	for i := range 3 {
		write(t, filepath.Join(dir, "f"), string(rune('a'+i)))
		time.Sleep(100 * time.Millisecond)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times behind another app", n)
	}
	m.SetFocused(true)
	eventually(t, "a catch-up read", func() bool { return reads.Load() == 2 })
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 2 {
		t.Fatalf("read %d times catching up, want one", n)
	}

	kept, keptReads := counted("k", true)
	Subscribe(m, kept, Need{MaxAge: 50 * time.Millisecond, Background: Keep}, "k")
	m.SetFocused(false)
	before := keptReads.Load()
	time.Sleep(300 * time.Millisecond)
	if n := keptReads.Load() - before; n < 3 {
		t.Errorf("a Keep subscription read %d times in 300ms behind another app", n)
	}
	slow, slowReads := counted("slow", true)
	Subscribe(m, slow, Need{MaxAge: 10 * time.Millisecond, Background: Slow, Slow: time.Hour}, "slow")
	eventually(t, "the slow source's first read", func() bool { return slowReads.Load() == 1 })
	time.Sleep(200 * time.Millisecond)
	if n := slowReads.Load(); n != 1 {
		t.Errorf("a Slow subscription read %d times in the background, want 1", n)
	}
}

// A source read from another is read again only when that one's reading changes, and after it.
func TestDerivedSourcesFollowTheirDeps(t *testing.T) {
	dir := t.TempDir()
	m := manager(t, Options{})
	var gen atomic.Int32
	base := Source[int]{Key: "base", Files: []Files{{Path: dir, Tree: true}}, Same: func(a, b int) bool { return a == b },
		Read: func(Reader) (int, error) { return int(gen.Load()), nil }}
	var derived atomic.Int32
	top := Source[int]{Key: "top", Deps: []AnySource{base}, Read: func(r Reader) (int, error) {
		derived.Add(1)
		v, ok := Dep(r, base)
		if !ok {
			return 0, errors.New("no base")
		}
		return v * 10, nil
	}}
	s := Subscribe(m, top, Need{}, "s")
	eventually(t, "the first read", func() bool { v, _, ok := s.Get(); return ok && v == 0 })
	write(t, filepath.Join(dir, "f"), "same")
	time.Sleep(200 * time.Millisecond)
	if n := derived.Load(); n != 1 {
		t.Fatalf("the derived source read %d times for a base reading that didn't change", n)
	}
	gen.Store(4)
	write(t, filepath.Join(dir, "f"), "new")
	eventually(t, "the derived reading", func() bool { v, _, _ := s.Get(); return v == 40 })
	if st := m.Stats(); len(st) != 2 {
		t.Errorf("stats list %d sources, want both", len(st))
	}
}

// A source nobody subscribes to keeps its watch and reading through its grace, then lets both go.
func TestLetsGoAfterItsGrace(t *testing.T) {
	dir := t.TempDir()
	m := manager(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true})
	s := Subscribe(m, src, Need{}, "s")
	eventually(t, "the first read", func() bool { return reads.Load() == 1 })
	s.Close()
	again := Subscribe(m, src, Need{}, "again")
	if _, _, ok := again.Get(); !ok {
		t.Fatal("a subscription back within the grace has no reading")
	}
	time.Sleep(100 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times coming back within the grace", n)
	}
	again.Close()
	eventually(t, "letting go", func() bool { return len(m.Stats()) == 0 })
	m.roots.mu.RLock()
	left := len(m.roots.files)
	m.roots.mu.RUnlock()
	if left != 0 {
		t.Errorf("%d paths still watched", left)
	}
}

// A paused subscription reads nothing until it resumes.
func TestPausedSubscriptionsReadNothing(t *testing.T) {
	m := manager(t, Options{})
	src, reads := counted("p", true)
	s := Subscribe(m, src, Need{MaxAge: 20 * time.Millisecond}, "s")
	eventually(t, "the first read", func() bool { return reads.Load() >= 1 })
	s.Pause()
	time.Sleep(50 * time.Millisecond)
	before := reads.Load()
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load() - before; n > 0 {
		t.Fatalf("read %d times paused", n)
	}
	s.Resume()
	eventually(t, "reads again", func() bool { return reads.Load() > before })
}

// Files that can't be watched are polled while someone subscribes.
func TestPollsWhatItCantWatch(t *testing.T) {
	m := manager(t, Options{Watch: func() (*fswatch.Watcher, error) { return nil, fswatch.ErrLimit }})
	src, reads := counted("w", false, Files{Path: t.TempDir(), Tree: true})
	s := Subscribe(m, src, Need{}, "s")
	time.Sleep(350 * time.Millisecond)
	if n := reads.Load(); n < 3 {
		t.Fatalf("read %d times in 350ms polling at 100ms", n)
	}
	s.Close()
}

// FilesSource's readings are what changed.
func TestFilesSourceSaysWhatChanged(t *testing.T) {
	dir := t.TempDir()
	m := manager(t, Options{})
	s := Subscribe(m, FilesSource("files", Files{Path: dir, Tree: true}), Need{}, "s")
	eventually(t, "the first read", func() bool { _, _, ok := s.Get(); return ok })
	write(t, filepath.Join(dir, "x"), "1")
	eventually(t, "x", func() bool { v, _, _ := s.Get(); return len(v) == 1 && v[0] == filepath.Join(dir, "x") })
}

// Stats say what made each source read last: the path that changed, or the source it reads from.
func TestStatsSayWhyASourceRead(t *testing.T) {
	m := manager(t, Options{})
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	write(t, f, "a")
	src, reads := counted("files", false, Files{Path: f})
	derived := Derive("derived", src, func(n int) (int, error) { return n, nil })
	s := Subscribe(m, derived, Need{}, "test")
	defer s.Close()
	eventually(t, "the first read", func() bool { _, _, ok := s.Get(); return ok })
	write(t, f, "b")
	eventually(t, "the change", func() bool { return reads.Load() == 2 })
	eventually(t, "the derived read", func() bool { v, _, _ := s.Get(); return v == 2 })
	why := map[string]string{}
	for _, st := range m.Stats() {
		why[st.Key] = st.Cause
	}
	if why["files"] != f || why["derived"] != "files" {
		t.Fatalf("causes %v, want files from %s and derived from files", why, f)
	}
}
