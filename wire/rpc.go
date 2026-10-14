package wire

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

// Conn is one multiplexed connection between the app and a worker. Either side calls, streams and handles:
// each call or stream is a stream of its own, so a slow one holds up nothing else.
type Conn interface {
	Call(ctx context.Context, method string, in, out any) error
	Stream(ctx context.Context, method string, in any) (Stream, error) // events, PTYs, logs
	Handle(method string, h Handler)
	HandleStream(method string, h StreamHandler)
	Close() error
	Done() <-chan struct{}
}

// Stream is the receiving end of a stream: Recv until io.EOF, or Close to stop it early.
type Stream interface {
	Recv(v any) error
	Close() error
}

// Handler answers a call. in is the caller's argument as JSON.
type Handler func(ctx context.Context, in json.RawMessage) (any, error)

// StreamHandler serves a stream, sending as many values as it likes. ctx ends when the receiver closes it.
type StreamHandler func(ctx context.Context, in json.RawMessage, send func(any) error) error

// Func adapts a typed function to a Handler.
func Func[In, Out any](fn func(ctx context.Context, in In) (Out, error)) Handler {
	return func(ctx context.Context, raw json.RawMessage) (any, error) {
		var in In
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, err
			}
		}
		return fn(ctx, in)
	}
}

// StreamFunc adapts a typed function to a StreamHandler.
func StreamFunc[In any](fn func(ctx context.Context, in In, send func(any) error) error) StreamHandler {
	return func(ctx context.Context, raw json.RawMessage, send func(any) error) error {
		var in In
		if len(raw) > 0 && string(raw) != "null" {
			if err := json.Unmarshal(raw, &in); err != nil {
				return err
			}
		}
		return fn(ctx, in, send)
	}
}

// RemoteError is an error a handler returned, as the caller sees it.
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return e.Message }

// ErrNoMethod is returned for a method nobody handles.
var ErrNoMethod = errors.New("no such method")

const frameMax = 64 << 20

type request struct {
	Method string          `json:"method"`
	Stream bool            `json:"stream,omitempty"`
	In     json.RawMessage `json:"in,omitempty"`
}

type reply struct {
	Out   json.RawMessage `json:"out,omitempty"`
	Error string          `json:"error,omitempty"`
	End   bool            `json:"end,omitempty"`
}

func writeFrame(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if len(b) > frameMax {
		return fmt.Errorf("frame too large: %d bytes", len(b))
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err = w.Write(buf)
	return err
}

func readFrame(r io.Reader, v any) error {
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > frameMax {
		return fmt.Errorf("frame too large: %d bytes", size)
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	return json.Unmarshal(b, v)
}

type conn struct {
	sess    *yamux.Session
	mu      sync.RWMutex
	calls   map[string]Handler
	streams map[string]StreamHandler
}

// New serves RPC over a yamux session, from either end.
func New(sess *yamux.Session) Conn {
	c := &conn{sess: sess, calls: map[string]Handler{}, streams: map[string]StreamHandler{}}
	go c.accept()
	return c
}

// Pipe is a connected pair in one process: a local worker, and tests.
func Pipe() (a, b Conn) {
	ra, rb := net.Pipe()
	sa, _ := yamux.Client(ra, yamuxConfig())
	sb, _ := yamux.Server(rb, yamuxConfig())
	return New(sa), New(sb)
}

// Client serves RPC over raw as the side that dialled (a unix socket, say), with no handshake.
func Client(raw net.Conn) (Conn, error) {
	s, err := yamux.Client(raw, yamuxConfig())
	if err != nil {
		return nil, err
	}
	return New(s), nil
}

// Server is Client for the side that accepted.
func Server(raw net.Conn) (Conn, error) {
	s, err := yamux.Server(raw, yamuxConfig())
	if err != nil {
		return nil, err
	}
	return New(s), nil
}

func (c *conn) Close() error          { return c.sess.Close() }
func (c *conn) Done() <-chan struct{} { return c.sess.CloseChan() }

func (c *conn) Handle(method string, h Handler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls[method] = h
}

func (c *conn) HandleStream(method string, h StreamHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.streams[method] = h
}

// open starts a call; done stops it, and ctx ending stops it early.
func (c *conn) open(ctx context.Context, method string, stream bool, in any) (s *yamux.Stream, done func(), err error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, nil, err
	}
	s, err = c.sess.OpenStream()
	if err != nil {
		return nil, nil, err
	}
	if err := writeFrame(s, request{Method: method, Stream: stream, In: raw}); err != nil {
		_ = s.Close()
		return nil, nil, err
	}
	stop := make(chan struct{})
	var once sync.Once
	done = func() { once.Do(func() { close(stop); _ = s.Close() }) }
	if ctx.Done() != nil {
		go func() {
			select {
			case <-ctx.Done():
				_ = s.Close()
			case <-stop:
			}
		}()
	}
	return s, done, nil
}

func (c *conn) Call(ctx context.Context, method string, in, out any) error {
	s, done, err := c.open(ctx, method, false, in)
	if err != nil {
		return err
	}
	defer done()
	var r reply
	err = readFrame(s, &r)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if r.Error != "" {
		return &RemoteError{Message: r.Error}
	}
	if out != nil && len(r.Out) > 0 {
		return json.Unmarshal(r.Out, out)
	}
	return nil
}

func (c *conn) Stream(ctx context.Context, method string, in any) (Stream, error) {
	s, done, err := c.open(ctx, method, true, in)
	if err != nil {
		return nil, err
	}
	return &recv{s: s, done: done, ctx: ctx}, nil
}

type recv struct {
	s    *yamux.Stream
	done func()
	ctx  context.Context
	err  error
}

func (r *recv) Recv(v any) error {
	if r.err != nil {
		return r.err
	}
	var rep reply
	if err := readFrame(r.s, &rep); err != nil {
		if r.ctx.Err() != nil {
			err = r.ctx.Err()
		}
		r.err = err
		r.done()
		return err
	}
	switch {
	case rep.Error != "":
		r.err = &RemoteError{Message: rep.Error}
	case rep.End:
		r.err = io.EOF
	default:
		return json.Unmarshal(rep.Out, v)
	}
	r.done()
	return r.err
}

func (r *recv) Close() error { r.done(); return nil }

func (c *conn) accept() {
	for {
		s, err := c.sess.AcceptStream()
		if err != nil {
			return
		}
		go c.serve(s)
	}
}

func (c *conn) serve(s *yamux.Stream) {
	defer s.Close()
	var req request
	if err := readFrame(s, &req); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The caller closing its end is how a call or stream is cancelled.
	go func() {
		var b [1]byte
		_, _ = s.Read(b[:])
		cancel()
	}()
	c.mu.RLock()
	h, hs := c.calls[req.Method], c.streams[req.Method]
	c.mu.RUnlock()
	if req.Stream {
		if hs == nil {
			_ = writeFrame(s, reply{Error: fmt.Sprintf("%s: %v", req.Method, ErrNoMethod)})
			return
		}
		var mu sync.Mutex
		send := func(v any) error {
			b, err := json.Marshal(v)
			if err != nil {
				return err
			}
			mu.Lock()
			defer mu.Unlock()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return writeFrame(s, reply{Out: b})
		}
		err := safely(func() error { return hs(ctx, req.In, send) })
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			_ = writeFrame(s, reply{Error: err.Error()})
		} else {
			_ = writeFrame(s, reply{End: true})
		}
		return
	}
	if h == nil {
		_ = writeFrame(s, reply{Error: fmt.Sprintf("%s: %v", req.Method, ErrNoMethod)})
		return
	}
	var out any
	err := safely(func() (err error) { out, err = h(ctx, req.In); return err })
	if err != nil {
		_ = writeFrame(s, reply{Error: err.Error()})
		return
	}
	b, err := json.Marshal(out)
	if err != nil {
		_ = writeFrame(s, reply{Error: err.Error()})
		return
	}
	_ = writeFrame(s, reply{Out: b})
}

// safely turns a handler's panic into an error, so one bad call doesn't take the connection down.
func safely(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("panic: %v", v)
		}
	}()
	return fn()
}
