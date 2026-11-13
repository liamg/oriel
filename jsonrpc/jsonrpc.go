// Package jsonrpc is JSON-RPC 2.0 over a byte stream, with LSP's Content-Length framing or one message a line.
package jsonrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"
)

// Error codes from JSON-RPC, then LSP's.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603

	// Overloaded refuses a request while maxRequests others wait on a reply.
	Overloaded = -32000

	RequestFailed    = -32803
	ServerCancelled  = -32802
	ContentModified  = -32801
	RequestCancelled = -32800
)

// maxRequests is how many of the other side's requests may wait on a reply at once.
const maxRequests = 256

// flushTimeout is how long Close waits for what's queued to be written.
const flushTimeout = time.Second

var (
	// ErrClosed is a call on a connection that has stopped.
	ErrClosed = errors.New("jsonrpc: connection closed")
	// ErrMethodNotFound, as a reply's error, answers MethodNotFound.
	ErrMethodNotFound = &Error{Code: MethodNotFound, Message: "method not found"}
)

// Error is a response's error.
type Error struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// RPCCode is its code, as a Coded error's.
func (e *Error) RPCCode() int { return int(e.Code) }

// Coded is an error of a caller's own that knows its JSON-RPC code: a reply sends it with that code and its own
// message.
type Coded interface {
	error
	RPCCode() int
}

// wireError is err as a reply sends it: an *Error as it is, a Coded one with its code, anything else an internal
// error.
func wireError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	var c Coded
	if errors.As(err, &c) {
		return &Error{Code: int64(c.RPCCode()), Message: err.Error()}
	}
	return &Error{Code: InternalError, Message: err.Error()}
}

// Message is any JSON-RPC message: a request has an ID and a Method, a notification only a Method, a response
// an ID and a Result or an Error.
type Message struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) IsRequest() bool      { return len(m.ID) > 0 && m.Method != "" }
func (m *Message) IsNotification() bool { return len(m.ID) == 0 && m.Method != "" }
func (m *Message) IsResponse() bool     { return len(m.ID) > 0 && m.Method == "" }

// IDKey is an id as a map key: the same number or string however it was spaced.
func IDKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, id) != nil {
		return string(id)
	}
	return buf.String()
}

// Request is a request from the other side. Reply answers it, now or later, from any goroutine.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage

	c      *Conn
	key    string
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

// Context ends when the request is answered, the other side cancels it, or the connection stops.
func (r *Request) Context() context.Context { return r.ctx }

// Reply sends the result, or the error: an *Error as it is, any other as InternalError. Only the first reply
// is sent.
func (r *Request) Reply(result any, err error) error {
	sent := false
	var serr error
	r.once.Do(func() {
		sent = true
		r.c.mu.Lock()
		r.c.inflight--
		delete(r.c.cancels, r.key)
		r.c.mu.Unlock()
		r.cancel()
		m := &Message{ID: r.ID}
		if err == nil {
			var b []byte
			if b, err = json.Marshal(result); err == nil {
				m.Result = b
			}
		}
		if err != nil {
			m.Error = wireError(err)
		}
		serr = r.c.send(m, nil)
	})
	if !sent {
		return errors.New("jsonrpc: request already answered")
	}
	return serr
}

// Answer is a request handler that replies with what f returns, before the next message is handled.
func Answer(f func(ctx context.Context, method string, params json.RawMessage) (any, error)) func(*Request) {
	return func(r *Request) { r.Reply(f(r.Context(), r.Method, r.Params)) }
}

// Handlers answer what the other side sends. Both are called on one goroutine in the order messages arrive, so
// neither may block for long: a request that waits replies from a goroutine of its own. While maxRequests wait on
// a reply, more are refused with Overloaded.
type Handlers struct {
	// Request handles a request; nil answers every request with MethodNotFound.
	Request func(r *Request)
	Notify  func(method string, params json.RawMessage)
}

// Cancel is a protocol's notification that cancels a request: its method, and the field of its params that holds
// the request's id.
type Cancel struct {
	Method string
	Field  string
}

// Options shape what a connection reads and writes.
type Options struct {
	// Framing cuts messages from the stream; Lines when unset.
	Framing Framing
	// Version is written as "jsonrpc" on every message; Codex's app-server leaves it out, LSP and MCP want "2.0".
	Version string
	// Cancel, when set, is sent for a call whose context ends, and cancels a request's Context when it arrives.
	Cancel *Cancel
	// ParseErrors answers a message that isn't JSON with ParseError, as a server must.
	ParseErrors bool
	// Queue makes a send return once it's queued, rather than once it's written, so it never waits on the other
	// side reading. Either way messages are written in the order sent.
	Queue bool
	// InOrder hands a call its result only once the handlers have seen what arrived before it, for a protocol whose
	// notifications lead up to a result (ACP's session/update before session/prompt's answer). A handler that
	// waits on a call of its own must then do so off the handler's goroutine.
	InOrder bool
}

// Conn is one connection. It is safe for concurrent use. What it sends is written in order by a goroutine of its own.
type Conn struct {
	r   io.Reader
	w   io.Writer
	h   Handlers
	opt Options

	ctx  context.Context // ends when the connection stops
	stop context.CancelFunc

	qmu     sync.Mutex
	queue   []outgoing
	writing bool
	flushed chan struct{} // closed once the writer has nothing left to write
	closing bool
	werr    error // why writes fail: Close, or a failed write. The other side going quiet doesn't stop replies.

	mu       sync.Mutex
	next     int64
	pending  map[string]chan *Message
	cancels  map[string]context.CancelFunc // the other side's requests, by id
	inflight int                           // the other side's requests not yet answered
	err      error                         // why calls fail: the other side stopped, or this one closed

	inbox chan inbound
	done  chan struct{}
}

type inbound struct {
	m        *Message
	r        *Request      // nil for a notification
	response chan *Message // a result to hand over in order (Options.InOrder)
}

// outgoing is a message queued to send, with its params still to marshal when they're given as a value.
type outgoing struct {
	m       *Message
	params  any
	written chan error // nil when the sender doesn't wait
}

func (o outgoing) done(err error) {
	if o.written != nil {
		o.written <- err
	}
}

// NewConn starts reading r. Close closes r and w when they are io.Closers.
func NewConn(r io.Reader, w io.Writer, h Handlers, opt Options) *Conn {
	if opt.Framing.Read == nil {
		opt.Framing = Lines
	}
	ctx, stop := context.WithCancel(context.Background())
	c := &Conn{r: r, w: w, h: h, opt: opt, ctx: ctx, stop: stop, pending: map[string]chan *Message{},
		cancels: map[string]context.CancelFunc{}, inbox: make(chan inbound, 64), done: make(chan struct{})}
	go c.read()
	go c.dispatch()
	return c
}

// Done is closed once the connection has stopped reading, every handler has returned and what they sent is written.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection stopped: ErrClosed, wrapping the read error if there was one.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close stops the connection once what's queued is written, waiting up to flushTimeout for it.
func (c *Conn) Close() error {
	c.qmu.Lock()
	c.closing = true
	c.qmu.Unlock()
	c.waitFlushed(time.After(flushTimeout))
	c.qmu.Lock()
	if c.werr == nil {
		c.werr = ErrClosed
	}
	c.qmu.Unlock()
	c.fail(ErrClosed)
	var err error
	if wc, ok := c.w.(io.Closer); ok {
		err = wc.Close()
	}
	if rc, ok := c.r.(io.Closer); ok {
		rc.Close()
	}
	return err
}

func (c *Conn) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.stop()
}

func (c *Conn) read() {
	defer close(c.inbox)
	br := bufio.NewReaderSize(c.r, 64<<10)
	for {
		body, err := c.opt.Framing.Read(br)
		if err != nil {
			if err == io.EOF || errors.Is(err, io.ErrClosedPipe) {
				err = ErrClosed
			} else {
				err = fmt.Errorf("%w: %w", ErrClosed, err)
			}
			c.fail(err)
			return
		}
		c.receive(body)
	}
}

func (c *Conn) receive(body []byte) {
	m := new(Message)
	if err := json.Unmarshal(body, m); err != nil {
		if c.opt.ParseErrors {
			c.post(&Message{ID: json.RawMessage("null"), Error: &Error{Code: ParseError, Message: "parse error"}})
		}
		return
	}
	switch {
	case m.Method == "":
		if len(m.ID) == 0 {
			return
		}
		key := IDKey(m.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch == nil {
			return
		}
		if c.opt.InOrder {
			c.inbox <- inbound{m: m, response: ch}
			return
		}
		ch <- m
	case len(m.ID) == 0:
		if c.opt.Cancel != nil && m.Method == c.opt.Cancel.Method {
			c.cancelled(m.Params)
		}
		c.inbox <- inbound{m: m}
	default:
		r := &Request{ID: m.ID, Method: m.Method, Params: m.Params, c: c, key: IDKey(m.ID)}
		c.mu.Lock()
		full := c.inflight >= maxRequests
		if !full {
			c.inflight++
			r.ctx, r.cancel = context.WithCancel(c.ctx)
			c.cancels[r.key] = r.cancel
		}
		c.mu.Unlock()
		if full {
			c.post(&Message{ID: m.ID, Error: &Error{Code: Overloaded, Message: "too many requests in flight"}})
			return
		}
		c.inbox <- inbound{m: m, r: r}
	}
}

// cancelled ends the Context of the request a cancel notification names.
func (c *Conn) cancelled(params json.RawMessage) {
	var p map[string]json.RawMessage
	if json.Unmarshal(params, &p) != nil || len(p[c.opt.Cancel.Field]) == 0 {
		return
	}
	c.mu.Lock()
	cancel := c.cancels[IDKey(p[c.opt.Cancel.Field])]
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// dispatch runs the handlers off the read loop, in order, so a handler can make calls of its own.
func (c *Conn) dispatch() {
	defer close(c.done)
	var queue []inbound
	in := c.inbox
	work := make(chan inbound)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for m := range work {
			c.handle(m)
		}
	}()
	for in != nil || len(queue) > 0 {
		var out chan inbound
		var head inbound
		if len(queue) > 0 {
			out, head = work, queue[0]
		}
		select {
		case m, ok := <-in:
			if !ok {
				in = nil
				continue
			}
			queue = append(queue, m)
		case out <- head:
			queue = queue[1:]
		}
	}
	close(work)
	<-finished
	c.waitFlushed(nil)
}

func (c *Conn) handle(in inbound) {
	if in.response != nil {
		in.response <- in.m
		return
	}
	if in.r == nil {
		if c.h.Notify != nil {
			c.h.Notify(in.m.Method, in.m.Params)
		}
		return
	}
	if c.h.Request == nil {
		in.r.Reply(nil, &Error{Code: MethodNotFound, Message: "method not found: " + in.m.Method})
		return
	}
	c.h.Request(in.r)
}

// send queues m, its params marshalled from params unless that's nil, behind everything sent before it. Unless the
// Options say Queue, it returns once m is written.
func (c *Conn) send(m *Message, params any) error { return c.queued(m, params, !c.opt.Queue) }

// post queues m without waiting for it to be written, for the read loop, which mustn't wait on the other side.
func (c *Conn) post(m *Message) { c.queued(m, nil, false) }

func (c *Conn) queued(m *Message, params any, wait bool) error {
	c.qmu.Lock()
	if c.werr != nil {
		defer c.qmu.Unlock()
		return c.werr
	}
	if c.closing {
		c.qmu.Unlock()
		return ErrClosed
	}
	o := outgoing{m: m, params: params}
	if wait {
		o.written = make(chan error, 1)
	}
	c.queue = append(c.queue, o)
	if !c.writing {
		c.writing = true
		c.flushed = make(chan struct{})
		go c.write()
	}
	c.qmu.Unlock()
	if o.written == nil {
		return nil
	}
	return <-o.written
}

// write writes what's queued, in order, until nothing is left or a write fails.
func (c *Conn) write() {
	for {
		c.qmu.Lock()
		if len(c.queue) == 0 || c.werr != nil {
			for _, o := range c.queue {
				o.done(c.werr)
			}
			c.queue = nil
			c.writing = false
			close(c.flushed)
			c.qmu.Unlock()
			return
		}
		q := c.queue
		c.queue = nil
		werr := c.werr
		c.qmu.Unlock()
		for _, o := range q {
			if werr != nil {
				o.done(werr)
				continue
			}
			b, err := c.encode(o)
			if err != nil {
				o.done(err)
				continue
			}
			if err := c.opt.Framing.Write(c.w, b); err != nil {
				werr = fmt.Errorf("%w: %w", ErrClosed, err)
				c.qmu.Lock()
				if c.werr == nil {
					c.werr = werr
				}
				werr = c.werr
				c.qmu.Unlock()
				c.fail(werr)
				o.done(werr)
				continue
			}
			o.done(nil)
		}
	}
}

func (c *Conn) encode(o outgoing) ([]byte, error) {
	if o.params != nil {
		p, err := marshalParams(o.params)
		if err != nil {
			return nil, err
		}
		o.m.Params = p
	}
	o.m.JSONRPC = c.opt.Version
	return json.Marshal(o.m)
}

// waitFlushed returns once nothing is queued or being written, or timeout fires.
func (c *Conn) waitFlushed(timeout <-chan time.Time) {
	c.qmu.Lock()
	if !c.writing {
		c.qmu.Unlock()
		return
	}
	ch := c.flushed
	c.qmu.Unlock()
	select {
	case <-ch:
	case <-timeout:
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	if raw, ok := params.(json.RawMessage); ok {
		return raw, nil
	}
	return json.Marshal(params)
}

// Notify sends a notification, its params marshalled when it's written.
func (c *Conn) Notify(method string, params any) error {
	return c.send(&Message{Method: method}, params)
}

// Call sends a request and decodes its result into result, if it isn't nil. If ctx ends first, Call sends the
// Options' Cancel, if there is one, and returns ctx's error.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	raw, err := c.CallRaw(ctx, method, params)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(raw, result)
}

// CallRaw is Call returning the result undecoded.
func (c *Conn) CallRaw(ctx context.Context, method string, params any) (json.RawMessage, error) {
	p, err := marshalParams(params)
	if err != nil {
		return nil, err
	}
	ch := make(chan *Message, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return nil, err
	}
	c.next++
	id := strconv.FormatInt(c.next, 10)
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(&Message{ID: json.RawMessage(id), Method: method, Params: p}, nil); err != nil {
		c.forget(id)
		return nil, err
	}
	select {
	case m, ok := <-ch:
		if !ok {
			return nil, c.Err()
		}
		if m.Error != nil {
			return nil, m.Error
		}
		if len(m.Result) == 0 {
			return json.RawMessage("null"), nil
		}
		return m.Result, nil
	case <-ctx.Done():
		c.forget(id)
		if c.opt.Cancel != nil {
			c.Notify(c.opt.Cancel.Method, map[string]json.RawMessage{c.opt.Cancel.Field: json.RawMessage(id)})
		}
		return nil, ctx.Err()
	}
}

func (c *Conn) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}
