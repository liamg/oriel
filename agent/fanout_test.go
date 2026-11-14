package agent

import (
	"bytes"
	"testing"
)

// A view that attaches gets what was shown lately, then each chunk as it comes, its own copy; closing ends every
// view, and one that attaches after gets the backlog and an ended stream.
func TestFanout(t *testing.T) {
	var f Fanout
	early := []byte("hello ")
	f.Feed(early)
	early[0] = 'J' // the caller's buffer, reused
	backlog, ch, detach := f.Attach()
	if string(backlog) != "hello " {
		t.Errorf("backlog %q", backlog)
	}
	f.Feed([]byte("world"))
	if got := <-ch; string(got) != "world" {
		t.Errorf("chunk %q", got)
	}
	detach()
	detach() // twice is harmless
	if _, open := <-ch; open {
		t.Error("a detached view's stream is still open")
	}
	_, live, _ := f.Attach()
	f.Close()
	if _, open := <-live; open {
		t.Error("Close left a view's stream open")
	}
	f.Feed([]byte("after"))
	backlog, ch, _ = f.Attach()
	if _, open := <-ch; open || !bytes.Equal(backlog, []byte("hello world")) {
		t.Errorf("after Close: backlog %q, stream open %v", backlog, open)
	}
}

// The backlog keeps the last FanoutBacklog bytes.
func TestFanoutBacklogIsBounded(t *testing.T) {
	var f Fanout
	f.Feed(bytes.Repeat([]byte("a"), FanoutBacklog))
	f.Feed(bytes.Repeat([]byte("b"), FanoutBacklog+10))
	backlog, _, _ := f.Attach()
	if len(backlog) != FanoutBacklog || backlog[0] != 'b' {
		t.Errorf("backlog of %d bytes starting %q", len(backlog), backlog[:1])
	}
}
