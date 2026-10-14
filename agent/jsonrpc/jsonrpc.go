// Package jsonrpc is JSON-RPC 2.0 with one JSON object per line: Codex's app-server and MCP's stdio transport.
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
)

// Error codes from JSON-RPC.
const (
	ParseError     = -32700
	InvalidRequest = -32600
	MethodNotFound = -32601
	InvalidParams  = -32602
	InternalError  = -32603
)

// ErrClosed is a call on a connection that has stopped.
var ErrClosed = errors.New("jsonrpc: connection closed")

// Error is a response's error.
type Error struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

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

// Request is a request from the other side. Reply answers it, now or later, from any goroutine.
type Request struct {
	ID     json.RawMessage
	Method string
	Params json.RawMessage

	c    *Conn
	once sync.Once
}

// Reply sends the result, or the error: an *Error as it is, any other as InternalError. Only the first reply
// is sent.
func (r *Request) Reply(result any, err error) error {
	sent := false
	var serr error
	r.once.Do(func() {
		sent = true
		m := &Message{ID: r.ID}
		if err == nil {
			var b []byte
			if b, err = json.Marshal(result); err == nil {
				m.Result = b
			}
		}
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				e = &Error{Code: InternalError, Message: err.Error()}
			}
			m.Error = e
		}
		serr = r.c.send(m)
	})
	if !sent {
		return errors.New("jsonrpc: request already answered")
	}
	return serr
}

// Handlers answer what the other side sends. Both are called on one goroutine in the order messages arrive, so
// neither may block for long: a request that waits replies from a goroutine of its own.
type Handlers struct {
	// Request handles a request; nil answers every request with MethodNotFound.
	Request func(r *Request)
	Notify  func(method string, params json.RawMessage)
}

// Options shape what a connection writes.
type Options struct {
	// Version is written as "jsonrpc" on every message; Codex's app-server leaves it out, MCP wants "2.0".
	Version string
}

// Conn is one connection. It is safe for concurrent use.
type Conn struct {
	r   io.Reader
	w   io.Writer
	h   Handlers
	opt Options

	wmu sync.Mutex

	mu      sync.Mutex
	next    int64
	pending map[string]chan *Message
	err     error // why calls fail: the other side stopped, or this one closed
	werr    error // why writes fail: Close, or a failed write. The other side going quiet doesn't stop replies.

	inbox chan *Message
	done  chan struct{}
}

// NewConn starts reading r. Close closes r and w when they are io.Closers.
func NewConn(r io.Reader, w io.Writer, h Handlers, opt Options) *Conn {
	c := &Conn{r: r, w: w, h: h, opt: opt, pending: map[string]chan *Message{}, inbox: make(chan *Message, 64),
		done: make(chan struct{})}
	go c.read()
	go c.dispatch()
	return c
}

// Done is closed once the connection has stopped and every handler has returned.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection stopped: ErrClosed, wrapping the read error if there was one.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close stops the connection.
func (c *Conn) Close() error {
	c.fail(ErrClosed)
	c.mu.Lock()
	if c.werr == nil {
		c.werr = ErrClosed
	}
	c.mu.Unlock()
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
}

func (c *Conn) read() {
	defer close(c.inbox)
	br := bufio.NewReaderSize(c.r, 64<<10)
	for {
		line, err := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			c.receive(line)
		}
		if err != nil {
			if err == io.EOF {
				c.fail(ErrClosed)
			} else {
				c.fail(fmt.Errorf("%w: %v", ErrClosed, err))
			}
			return
		}
	}
}

func (c *Conn) receive(line []byte) {
	m := new(Message)
	if err := json.Unmarshal(line, m); err != nil {
		return
	}
	if m.Method == "" {
		if len(m.ID) == 0 {
			return
		}
		key := idKey(m.ID)
		c.mu.Lock()
		ch := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if ch != nil {
			ch <- m
		}
		return
	}
	c.inbox <- m
}

// dispatch runs the handlers off the read loop, in order, so a handler can make calls of its own.
func (c *Conn) dispatch() {
	defer close(c.done)
	var queue []*Message
	in := c.inbox
	work := make(chan *Message)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for m := range work {
			c.handle(m)
		}
	}()
	for in != nil || len(queue) > 0 {
		var out chan *Message
		var head *Message
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
}

func (c *Conn) handle(m *Message) {
	if len(m.ID) == 0 {
		if c.h.Notify != nil {
			c.h.Notify(m.Method, m.Params)
		}
		return
	}
	r := &Request{ID: m.ID, Method: m.Method, Params: m.Params, c: c}
	if c.h.Request == nil {
		r.Reply(nil, &Error{Code: MethodNotFound, Message: "method not found: " + m.Method})
		return
	}
	c.h.Request(r)
}

func (c *Conn) send(m *Message) error {
	m.JSONRPC = c.opt.Version
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.mu.Lock()
	werr := c.werr
	c.mu.Unlock()
	if werr != nil {
		return werr
	}
	if _, err := c.w.Write(b); err != nil {
		err = fmt.Errorf("%w: %v", ErrClosed, err)
		c.mu.Lock()
		c.werr = err
		c.mu.Unlock()
		c.fail(err)
		return err
	}
	return nil
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

// Notify sends a notification.
func (c *Conn) Notify(method string, params any) error {
	p, err := marshalParams(params)
	if err != nil {
		return err
	}
	return c.send(&Message{Method: method, Params: p})
}

// Call sends a request and decodes its result into result, if it isn't nil.
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
	if err := c.send(&Message{ID: json.RawMessage(id), Method: method, Params: p}); err != nil {
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
		return nil, ctx.Err()
	}
}

func (c *Conn) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// idKey is an id as a map key: the same number or string however it was spaced.
func idKey(id json.RawMessage) string {
	var buf bytes.Buffer
	if json.Compact(&buf, id) != nil {
		return string(id)
	}
	return buf.String()
}
