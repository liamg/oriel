package watch

import (
	"sync/atomic"
	"testing"
	"time"
)

// Refresh reads a source and what it reads from again.
func TestRefreshReadsTheChainAgain(t *testing.T) {
	m := manager(t, Options{})
	var base atomic.Int32
	b := Source[int]{Key: "b", Files: []Files{{Path: t.TempDir(), Tree: true}}, Read: func(Reader) (int, error) { return int(base.Add(1)), nil }}
	var top atomic.Int32
	src := Source[int]{Key: "t", Deps: []AnySource{b}, Read: func(Reader) (int, error) { return int(top.Add(1)), nil }}
	s := Subscribe(m, src, Need{MaxAge: time.Second}, "s")
	eventually(t, "the first read", func() bool { return top.Load() == 1 })
	s.Refresh()
	if !s.Busy() {
		t.Error("not busy after a refresh")
	}
	eventually(t, "the refresh", func() bool { return base.Load() == 2 && top.Load() == 2 })
	eventually(t, "settled", func() bool { return !s.Busy() })
}
