package tailbuf

import (
	"strings"
	"testing"
)

// A buffer keeps the last Max bytes written to it, and says when it dropped some.
func TestBufferKeepsTheTail(t *testing.T) {
	b := &Buffer{Max: 8}
	b.Write([]byte("abc"))
	if b.String() != "abc" || b.Cut() {
		t.Errorf("under the limit: %q cut=%v", b.String(), b.Cut())
	}
	b.Write([]byte("defghij"))
	b.Write([]byte(strings.Repeat("x", 3)))
	if got := b.String(); got != "fghijxxx" || !b.Cut() {
		t.Errorf("over the limit: %q cut=%v", got, b.Cut())
	}
	if n, err := b.Write([]byte(strings.Repeat("y", 20))); n != 20 || err != nil || b.String() != strings.Repeat("y", 8) {
		t.Errorf("a write larger than Max: n=%d err=%v %q", n, err, b.String())
	}
}

// A buffer with no Max keeps everything.
func TestBufferWithNoMaxKeepsAll(t *testing.T) {
	b := &Buffer{}
	b.Write([]byte(strings.Repeat("a", 100)))
	if got := b.String(); len(got) != 100 || b.Cut() {
		t.Errorf("kept %d bytes, cut=%v", len(got), b.Cut())
	}
}
