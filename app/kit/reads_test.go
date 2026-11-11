package kit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/fswatch"
)

// A read lands at the next frame though no view asks for it again: the view that asked has gone.
func TestAReadLandsWithoutAnotherAsk(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	frame := 1
	woke := make(chan struct{}, 8)
	rd := NewReads(ReadsOpts{Frame: func() int { return frame }, Wake: func() { woke <- struct{}{} },
		Watch: func() (*fswatch.Watcher, error) { return nil, fswatch.ErrUnsupported }})
	t.Cleanup(rd.Close)
	if _, st := rd.File(p); !st.Loading() {
		t.Fatalf("the first read = %+v, want it under way", st)
	}
	<-woke // landed
	frame++
	if !rd.Busy() {
		t.Fatal("a read landed and not taken in, and the reads aren't busy")
	}
	rd.Land()
	if rd.Busy() {
		t.Errorf("busy once the read is taken in: %v", rd.Pending())
	}
}
