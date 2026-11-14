package agent

import "sync"

// FanoutBacklog is how much of what a terminal showed a view that attaches gets.
const FanoutBacklog = 256 << 10

// Fanout is a terminal's output as its views watch it (Terminal.Attach): what it showed lately, for a view that
// attaches, then each chunk as it comes. A view too slow to keep up drops chunks rather than hold up the output.
// The zero Fanout is ready to use.
type Fanout struct {
	mu      sync.Mutex
	buf     []byte
	viewers map[chan []byte]struct{}
	closed  bool
}

// Feed shows p to every view, and keeps it for those that attach later. p is copied, so its caller may reuse it.
// After Close it does nothing.
func (f *Fanout) Feed(p []byte) {
	chunk := append([]byte(nil), p...)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	f.buf = append(f.buf, chunk...)
	if len(f.buf) > 2*FanoutBacklog {
		f.buf = append([]byte(nil), f.buf[len(f.buf)-FanoutBacklog:]...)
	}
	for ch := range f.viewers {
		select {
		case ch <- chunk:
		default:
		}
	}
}

// Close ends every view's stream: the terminal has ended.
func (f *Fanout) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for ch := range f.viewers {
		close(ch)
	}
	f.viewers = nil
}

// Attach is Terminal.Attach: the backlog, a stream of what's shown from now on, and a detach that ends it.
func (f *Fanout) Attach() (backlog []byte, stream <-chan []byte, detach func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	backlog = f.buf
	if len(backlog) > FanoutBacklog {
		backlog = backlog[len(backlog)-FanoutBacklog:]
	}
	backlog = append([]byte(nil), backlog...)
	ch := make(chan []byte, 256)
	if f.closed {
		close(ch)
		return backlog, ch, func() {}
	}
	if f.viewers == nil {
		f.viewers = map[chan []byte]struct{}{}
	}
	f.viewers[ch] = struct{}{}
	return backlog, ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.viewers[ch]; ok {
			delete(f.viewers, ch)
			close(ch)
		}
	}
}
