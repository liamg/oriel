package claude

import (
	"github.com/liamg/oriel/agent"
)

// terminal is a session's PTY as views watch it. It's drained all the time, so claude never blocks on output.
type terminal struct {
	pty   agent.PTY
	watch func([]byte) // sees all it shows, before any view
	typed func([]byte) // sees all typed into it

	agent.Fanout // what views see: Attach
}

func newTerminal(p agent.PTY) *terminal { return &terminal{pty: p} }

// drain reads the PTY until it ends.
func (t *terminal) drain() {
	b := make([]byte, 32<<10)
	for {
		n, err := t.pty.Read(b)
		if n > 0 {
			if t.watch != nil {
				t.watch(b[:n])
			}
			t.Feed(b[:n])
		}
		if err != nil {
			break
		}
	}
	t.Close()
}

func (t *terminal) Write(p []byte) (int, error) {
	if t.typed != nil {
		t.typed(p)
	}
	return t.pty.Write(p)
}
func (t *terminal) Resize(cols, rows int) error { return t.pty.Resize(cols, rows) }
