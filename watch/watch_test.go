package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/internal/clock/clocktest"
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

// rig is a manager on a clock the test moves and a file watcher whose events the test sends: what's timed happens
// exactly when the test moves the clock past it, and nothing the disk does reaches it.
type rig struct {
	t      *testing.T
	m      *Manager
	clock  *clocktest.Fake
	script *fswatch.Script
}

func newRig(t *testing.T, o Options) *rig {
	t.Helper()
	r := &rig{t: t, clock: clocktest.New(time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC))}
	o.Clock = r.clock
	if o.Watch == nil {
		o.Watch = func() (*fswatch.Watcher, error) {
			w, s := fswatch.NewScripted()
			r.script = s
			return w, nil
		}
	}
	r.m = manager(t, o)
	r.settle()
	return r
}

// settle waits for every read under way to finish: not for a while, for that.
func (r *rig) settle() {
	r.t.Helper()
	eventually(r.t, "the manager settled", func() bool {
		r.m.mu.Lock()
		defer r.m.mu.Unlock()
		for _, n := range r.m.nodes {
			if n.reading {
				return false
			}
		}
		return true
	})
}

// advance moves the clock on by d, one thing due at a time, letting what each starts finish before the next.
func (r *rig) advance(d time.Duration) {
	r.t.Helper()
	r.settle()
	end := r.clock.Now().Add(d)
	for {
		next, ok := r.clock.Next()
		if !ok || next.After(end) {
			r.clock.Advance(end.Sub(r.clock.Now()))
			r.settle()
			return
		}
		r.clock.Advance(max(0, next.Sub(r.clock.Now())))
		r.settle()
	}
}

// change says paths changed, as the file watcher would once they'd settled, once the manager has taken them in.
func (r *rig) change(paths ...string) {
	r.t.Helper()
	if r.script == nil {
		r.t.Fatal("nothing is watching files yet")
	}
	r.script.Change(paths...)
	r.settle()
}

// A polled source is read at its subscriptions' tightest MaxAge, shared between them, and not at all once none is left.
func TestPollsAtTheTightestNeedAndStopsWithNoSubscribers(t *testing.T) {
	r := newRig(t, Options{})
	src, reads := counted("p", true)
	a := Subscribe(r.m, src, Need{MaxAge: time.Hour}, "a")
	r.advance(0)
	if _, _, ok := a.Get(); !ok {
		t.Fatal("no first read")
	}
	r.advance(150 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times at an hour's need, want 1", n)
	}
	b := Subscribe(r.m, src, Need{MaxAge: 50 * time.Millisecond}, "b")
	r.advance(300 * time.Millisecond)
	if n := reads.Load(); n != 8 { // at once for b's need (150ms since the first), then every 50ms to 450ms
		t.Fatalf("read %d times in 300ms at 50ms, want 8 in all", n)
	}
	if va, _, _ := a.Get(); va != 8 {
		t.Errorf("a has reading %d, not the shared 8", va)
	}
	a.Close()
	b.Close()
	before := reads.Load()
	r.advance(time.Hour)
	if n := reads.Load() - before; n > 0 {
		t.Errorf("read %d times with no subscribers", n)
	}
}

// A watched source is read when its files change, once for a burst, and not for what it skips or while nothing changes.
func TestReadsAWatchedSourceWhenItsFilesChange(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true, Skip: func(rel string) bool { return rel == "skip" }})
	s := Subscribe(r.m, src, Need{}, "s")
	r.advance(0)
	r.advance(time.Hour)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times with nothing changed, want the first read only", n)
	}
	r.change(filepath.Join(dir, "skip", "x"))
	r.advance(time.Hour)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times for a skipped folder", n)
	}
	for range 5 { // a burst: each closer to the last than the settle
		r.change(filepath.Join(dir, "a.txt"))
		r.advance(quick.Settle / 2)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times during a burst", n)
	}
	r.advance(quick.Settle)
	if n := reads.Load(); n != 2 {
		t.Fatalf("read %d times once the burst settled, want 2 in all", n)
	}
	r.advance(time.Hour)
	if n := reads.Load(); n != 2 {
		t.Errorf("read %d times for one burst, want 2 in all", n)
	}
	select {
	case <-s.Changed():
	default:
		t.Error("Changed didn't fire")
	}
}

// A burst that goes on is read at most Most after it began, not held back for as long as it lasts.
func TestALongBurstIsReadAtMost(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true})
	Subscribe(r.m, src, Need{}, "s")
	r.advance(0)
	for range 2 * int(quick.Most/(quick.Settle/2)) {
		r.change(filepath.Join(dir, "log"))
		r.advance(quick.Settle / 2)
	}
	if n := reads.Load(); n < 2 {
		t.Errorf("a burst twice Most long was read %d times, want at least once while it went on", n)
	}
}

// Behind another app a Pause subscription reads nothing, and once back the stale source is read once; Keep goes on.
func TestPausesInTheBackgroundAndCatchesUpOnce(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true})
	Subscribe(r.m, src, Need{}, "s")
	r.advance(0)
	r.m.SetFocused(false)
	for range 3 {
		r.change(filepath.Join(dir, "f"))
		r.advance(100 * time.Millisecond)
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times behind another app", n)
	}
	r.m.SetFocused(true)
	r.advance(time.Hour)
	if n := reads.Load(); n != 2 {
		t.Fatalf("read %d times catching up, want one", n)
	}

	kept, keptReads := counted("k", true)
	Subscribe(r.m, kept, Need{MaxAge: 50 * time.Millisecond, Background: Keep}, "k")
	r.advance(0)
	r.m.SetFocused(false)
	before := keptReads.Load()
	r.advance(300 * time.Millisecond)
	if n := keptReads.Load() - before; n != 6 {
		t.Errorf("a Keep subscription read %d times in 300ms behind another app, want every 50ms: 6", n)
	}
	slow, slowReads := counted("slow", true)
	Subscribe(r.m, slow, Need{MaxAge: 10 * time.Millisecond, Background: Slow, Slow: time.Hour}, "slow")
	r.advance(200 * time.Millisecond)
	if n := slowReads.Load(); n != 1 {
		t.Errorf("a Slow subscription read %d times in the background in 200ms, want only the first", n)
	}
}

// A source read from another is read again only when that one's reading changes, and after it.
func TestDerivedSourcesFollowTheirDeps(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, Options{})
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
	s := Subscribe(r.m, top, Need{}, "s")
	r.advance(time.Hour)
	if v, _, ok := s.Get(); !ok || v != 0 {
		t.Fatalf("the first reading: %v, %v", v, ok)
	}
	r.change(filepath.Join(dir, "f"))
	r.advance(time.Hour)
	if n := derived.Load(); n != 1 {
		t.Fatalf("the derived source read %d times for a base reading that didn't change", n)
	}
	gen.Store(4)
	r.change(filepath.Join(dir, "f"))
	r.advance(time.Hour)
	if v, _, _ := s.Get(); v != 40 {
		t.Errorf("the derived reading is %d, want 40", v)
	}
	if st := r.m.Stats(); len(st) != 2 {
		t.Errorf("stats list %d sources, want both", len(st))
	}
}

// A source nobody subscribes to keeps its watch and reading through its grace, then lets both go.
func TestLetsGoAfterItsGrace(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, Options{})
	src, reads := counted("w", false, Files{Path: dir, Tree: true})
	s := Subscribe(r.m, src, Need{}, "s")
	r.advance(0)
	s.Close()
	r.advance(quick.Grace / 2)
	again := Subscribe(r.m, src, Need{}, "again")
	if _, _, ok := again.Get(); !ok {
		t.Fatal("a subscription back within the grace has no reading")
	}
	r.advance(time.Hour)
	if n := reads.Load(); n != 1 {
		t.Fatalf("read %d times coming back within the grace", n)
	}
	again.Close()
	r.advance(quick.Grace - time.Millisecond)
	if len(r.m.Stats()) == 0 {
		t.Fatal("let go before its grace was over")
	}
	r.advance(time.Millisecond)
	if n := len(r.m.Stats()); n != 0 {
		t.Fatalf("%d sources kept past their grace", n)
	}
	r.m.roots.mu.RLock()
	left := len(r.m.roots.files)
	r.m.roots.mu.RUnlock()
	if left != 0 {
		t.Errorf("%d paths still watched", left)
	}
}

// A paused subscription reads nothing until it resumes.
func TestPausedSubscriptionsReadNothing(t *testing.T) {
	r := newRig(t, Options{})
	src, reads := counted("p", true)
	s := Subscribe(r.m, src, Need{MaxAge: 20 * time.Millisecond}, "s")
	r.advance(0)
	s.Pause()
	before := reads.Load()
	r.advance(time.Hour)
	if n := reads.Load() - before; n > 0 {
		t.Fatalf("read %d times paused", n)
	}
	s.Resume()
	r.advance(0)
	if reads.Load() == before {
		t.Error("resumed, it reads nothing")
	}
}

// Files that can't be watched are polled while someone subscribes.
func TestPollsWhatItCantWatch(t *testing.T) {
	r := newRig(t, Options{Watch: func() (*fswatch.Watcher, error) { return nil, fswatch.ErrLimit }})
	src, reads := counted("w", false, Files{Path: t.TempDir(), Tree: true})
	s := Subscribe(r.m, src, Need{}, "s")
	r.advance(350 * time.Millisecond)
	if n := reads.Load(); n != 4 { // at once, then every Fallback: 100, 200 and 300ms
		t.Fatalf("read %d times in 350ms polling at 100ms, want 4", n)
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

// A file's source is its contents: nil while there's no file, then each change as it's written, and nil once it's
// gone.
func TestFileIsWhatTheFileHolds(t *testing.T) {
	m := manager(t, Options{})
	p := filepath.Join(t.TempDir(), "state.json")
	s := Subscribe(m, File(p), Need{}, "test")
	got := func() []byte { b, _, _ := s.Get(); return b }
	eventually(t, "the first reading", func() bool { _, _, ok := s.Get(); return ok })
	if b := got(); b != nil {
		t.Fatalf("with no file the reading is %q, want nil", b)
	}
	write(t, p, "one")
	eventually(t, "the file as written", func() bool { return string(got()) == "one" })
	write(t, p, "two")
	eventually(t, "the file as changed", func() bool { return string(got()) == "two" })
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	eventually(t, "nothing once it's gone", func() bool { return got() == nil })
}

// Fresh is a reading begun after it's asked for, never the one a source kept from before: a file written since is
// read as it is now.
func TestFreshIsReadAfterItsAsked(t *testing.T) {
	m := manager(t, Options{Timing: Timing{Settle: time.Hour, Most: time.Hour, Grace: time.Hour, Fallback: time.Hour}})
	p := filepath.Join(t.TempDir(), "settings.toml")
	write(t, p, "a = 1")
	s := Subscribe(m, File(p), Need{}, "test")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if b, err := s.Fresh(ctx); err != nil || string(b) != "a = 1" {
		t.Fatalf("Fresh = %q, %v", b, err)
	}
	write(t, p, "a = 2") // no watch event reads it: the settle is an hour
	if b, err := s.Fresh(ctx); err != nil || string(b) != "a = 2" {
		t.Fatalf("Fresh after a write = %q, %v", b, err)
	}
}
