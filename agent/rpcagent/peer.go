package rpcagent

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/jsonrpc"
)

// Peer is a connection to the other side.
type Peer interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Notify(method string, params any) error
	Done() <-chan struct{} // closed once the connection has stopped
	Close() error
}

// Handler answers what the other side sends. Request runs on a goroutine of its own; Notify runs in the order
// notifications arrive, so it mustn't block.
type Handler struct {
	Request func(ctx context.Context, method string, params json.RawMessage) (any, error)
	Notify  func(method string, params json.RawMessage)
}

// Dialer connects to a plugin, its messages going to h: it starts the plugin's process, or opens its connection.
type Dialer func(ctx context.Context, h Handler) (Peer, error)

// NewConn is the protocol over r and w, a message a line, its messages going to h: a call's result after the
// notifications sent before it, as the protocol promises (an event before the call that ends its session).
func NewConn(r io.Reader, w io.Writer, h Handler) Peer {
	return &conn{c: jsonrpc.NewConn(r, w, jsonrpc.Handlers{
		Request: func(req *jsonrpc.Request) {
			go func() {
				res, err := h.Request(req.Context(), req.Method, req.Params)
				req.Reply(res, WireError(err))
			}()
		},
		Notify: h.Notify,
	}, jsonrpc.Options{Version: "2.0", Cancel: CancelRequest(), InOrder: true})}
}

// Pipe is a Dialer over r and w, which it can dial once.
func Pipe(r io.Reader, w io.Writer) Dialer {
	var once sync.Once
	return func(_ context.Context, h Handler) (Peer, error) {
		var p Peer
		once.Do(func() { p = NewConn(r, w, h) })
		if p == nil {
			return nil, jsonrpc.ErrClosed
		}
		return p, nil
	}
}

type conn struct{ c *jsonrpc.Conn }

func (c *conn) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return c.c.CallRaw(ctx, method, params)
}
func (c *conn) Notify(method string, params any) error { return c.c.Notify(method, params) }
func (c *conn) Done() <-chan struct{}                  { return c.c.Done() }
func (c *conn) Close() error                           { return c.c.Close() }

// events is a session's events as its channel gives them: never holding up whoever says one, and closed once the
// last is taken (end) or at once (drop).
type events struct {
	ch      chan agent.Event
	mu      sync.Mutex
	over    []agent.Event
	pumping bool
	ending  bool
	closed  bool
	abandon chan struct{}
	once    sync.Once
}

func newEvents() *events {
	return &events{ch: make(chan agent.Event, 256), abandon: make(chan struct{})}
}

func (q *events) push(ev agent.Event) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ending || q.closed {
		return
	}
	if !q.pumping {
		select {
		case q.ch <- ev:
			return
		default:
		}
		q.pumping = true
		go q.pump()
	}
	q.over = append(q.over, ev)
}

func (q *events) pump() {
	for {
		q.mu.Lock()
		if len(q.over) == 0 {
			q.pumping = false
			if q.ending {
				q.closeLocked()
			}
			q.mu.Unlock()
			return
		}
		ev := q.over[0]
		q.mu.Unlock()
		select {
		case q.ch <- ev:
			q.mu.Lock()
			q.over = q.over[1:]
			q.mu.Unlock()
		case <-q.abandon:
			q.mu.Lock()
			q.over, q.pumping = nil, false
			q.closeLocked()
			q.mu.Unlock()
			return
		}
	}
}

func (q *events) closeLocked() {
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}

// end closes the channel once what's said so far is taken.
func (q *events) end() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ending = true
	if !q.pumping {
		q.closeLocked()
	}
}

// drop closes the channel now, dropping what isn't taken past its buffer.
func (q *events) drop() {
	q.mu.Lock()
	q.ending = true
	if !q.pumping {
		q.closeLocked()
	}
	q.mu.Unlock()
	q.once.Do(func() { close(q.abandon) })
}

// stream is bytes written by one side and read by another: Write never blocks, Read waits for some.
type stream struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool // no more will be written
	gone   bool // the reader closed it
}

func newStream() *stream {
	s := &stream{}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *stream) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.gone {
		s.buf = append(s.buf, p...)
		s.cond.Broadcast()
	}
	return len(p), nil
}

func (s *stream) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.buf) == 0 && !s.closed && !s.gone {
		s.cond.Wait()
	}
	if s.gone {
		return 0, io.ErrClosedPipe
	}
	if len(s.buf) == 0 {
		return 0, io.EOF
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

// finish says nothing more will be written.
func (s *stream) finish() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Close is the reader giving up on it.
func (s *stream) Close() error {
	s.mu.Lock()
	s.gone, s.buf = true, nil
	s.cond.Broadcast()
	s.mu.Unlock()
	return nil
}
