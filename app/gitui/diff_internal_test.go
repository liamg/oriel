package gitui

import (
	"testing"

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
