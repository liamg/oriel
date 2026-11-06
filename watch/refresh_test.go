package watch

import (
	"sync/atomic"
	"testing"
	"time"
)

// Refresh reads a source and what it reads from again.
func TestRefreshReadsTheChainAgain(t *testing.T) {
	r := newRig(t, Options{})
	var base atomic.Int32
	b := Source[int]{Key: "b", Files: []Files{{Path: t.TempDir(), Tree: true}}, Read: func(Reader) (int, error) { return int(base.Add(1)), nil }}
	var top atomic.Int32
	src := Source[int]{Key: "t", Deps: []AnySource{b}, Read: func(Reader) (int, error) { return int(top.Add(1)), nil }}
	s := Subscribe(r.m, src, Need{MaxAge: time.Second}, "s")
	r.advance(0)
	if top.Load() != 1 {
		t.Fatalf("read %d times at first", top.Load())
	}
	s.Refresh()
	if !s.Busy() {
		t.Error("not busy after a refresh")
	}
	r.advance(0)
	if base.Load() != 2 || top.Load() != 2 {
		t.Errorf("after the refresh, base read %d times and the source %d, want 2 each", base.Load(), top.Load())
	}
	if s.Busy() {
		t.Error("busy once the refresh is read")
	}
}
