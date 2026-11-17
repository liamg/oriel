package gitui

import (
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/text"
)

// The diff's own buffer of a file's new side is edited to each new version, so an anchor in it (a thread's) stays
// on its line when lines above it change.
func TestDiffBufferFollowsEdits(t *testing.T) {
	x := &Diff{}
	st := &diffState{bufs: map[string]diffBuf{}}
	one := &diffFile{newText: []byte("a\nb\nc\nd\n")}
	buf := x.newBuffer(st, "f", one)
	an := buf.Anchor(buf.Snapshot().LineStart(3), text.Left)
	two := &diffFile{newText: []byte("a\nnew\nb\nc\nd\n")}
	if x.newBuffer(st, "f", two) != buf {
		t.Fatal("a new version made a new buffer")
	}
	if got := string(buf.Snapshot().Bytes()); got != string(two.newText) {
		t.Fatalf("the buffer reads %q", got)
	}
	off, _ := buf.Resolve(an)
	if line := buf.Snapshot().PosAt(off).Line; line != 4 {
		t.Errorf("the anchor on d is on line %d", line)
	}
}

// redrawHost is a host that counts the redraws asked of it.
type redrawHost struct {
	plugin.Host
	redraws int
}

func (h *redrawHost) Redraw() { h.redraws++ }

// A file's diff is remade at most every diffRemakeGap: one asked for sooner waits, and a frame is asked for as the
// gap ends, once however often it's asked meanwhile.
func TestDiffRemakesArePaced(t *testing.T) {
	clk := clocktest.New(time.Unix(1000, 0))
	h := &redrawHost{}
	x := &Diff{h: h, clock: clk, started: map[diffKey]time.Time{}, waking: map[diffKey]bool{}}
	k := diffKey{"/repo", "round.go", false}
	if !x.mayRemake(k) {
		t.Fatal("a diff never made waits")
	}
	x.started[k] = clk.Now()
	clk.Advance(diffRemakeGap / 2)
	for range 2 { // asked again, still within it
		if x.mayRemake(k) {
			t.Fatal("remade within the gap")
		}
	}
	clk.Advance(diffRemakeGap / 2)
	if h.redraws != 1 {
		t.Errorf("%d frames asked for as the gap ended, want 1", h.redraws)
	}
	if !x.mayRemake(k) {
		t.Error("the gap is over, and it still waits")
	}
}
