package claude

import (
	"sync"

	"github.com/liamg/oriel/agent"
)

// backlogSize is how much of what the terminal showed a newly attached view gets.
const backlogSize = 256 << 10

// terminal is a session's PTY as views watch it. It's drained all the time, so claude never blocks on output.
type terminal struct {
	pty agent.PTY

	mu      sync.Mutex
	buf     []byte
	viewers map[chan []byte]struct{}
	closed  bool
}

func newTerminal(p agent.PTY) *terminal {
	return &terminal{pty: p, viewers: map[chan []byte]struct{}{}}
}

// drain reads the PTY until it ends.
func (t *terminal) drain() {
	b := make([]byte, 32<<10)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			t.feed(b[:n])
		}
		if err != nil {
			break
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for ch := range t.viewers {
		close(ch)
	}
	t.viewers = nil
}

func (t *terminal) feed(p []byte) {
	chunk := append([]byte(nil), p...)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, chunk...)
	if len(t.buf) > 2*backlogSize {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-backlogSize:]...)
	}
	for ch := range t.viewers {
		select {
		case ch <- chunk:
		default: // a slow view drops output rather than holding up the agent
		}
	}
}

func (t *terminal) Write(p []byte) (int, error) { return t.pty.Write(p) }
func (t *terminal) Resize(cols, rows int) error { return t.pty.Resize(cols, rows) }

func (t *terminal) Attach() ([]byte, <-chan []byte, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	backlog := t.buf
	if len(backlog) > backlogSize {
		backlog = backlog[len(backlog)-backlogSize:]
	}
	backlog = append([]byte(nil), backlog...)
	ch := make(chan []byte, 256)
	if t.closed {
		close(ch)
		return backlog, ch, func() {}
	}
	t.viewers[ch] = struct{}{}
	return backlog, ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.viewers[ch]; ok {
			delete(t.viewers, ch)
			close(ch)
		}
	}
}
