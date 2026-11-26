// Package tailbuf is a writer that keeps only the end of what's written to it, for a program's output that may run
// long.
package tailbuf

import "sync"

// Buffer keeps the last Max bytes written to it, or all of them when Max is 0. It's safe for a program's stdout
// and stderr to share.
type Buffer struct {
	Max int
	mu  sync.Mutex
	b   []byte
	cut bool
}

func (t *Buffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if t.Max > 0 && len(t.b) > t.Max {
		t.b = append(t.b[:0], t.b[len(t.b)-t.Max:]...)
		t.cut = true
	}
	return len(p), nil
}

// String is what's kept.
func (t *Buffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.b)
}

// Cut says whether earlier output was dropped.
func (t *Buffer) Cut() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cut
}
