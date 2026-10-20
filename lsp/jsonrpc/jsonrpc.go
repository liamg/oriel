// Package jsonrpc is JSON-RPC 2.0 over a byte stream with LSP's Content-Length framing.
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
	"strings"
	"sync"
)

// Error codes from JSON-RPC and LSP.
const (
	ParseError       = -32700
	InvalidRequest   = -32600
	MethodNotFound   = -32601
	InvalidParams    = -32602
	InternalError    = -32603
	RequestFailed    = -32803
	ServerCancelled  = -32802
	ContentModified  = -32801
	RequestCancelled = -32800
)

var (
	ErrClosed = errors.New("jsonrpc: connection closed")
	// ErrMethodNotFound, returned by a request handler, answers MethodNotFound.
	ErrMethodNotFound = &Error{Code: MethodNotFound, Message: "method not found"}
)

// Error is a response's error.
type Error struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("jsonrpc %d: %s", e.Code, e.Message) }

// ID is a request id: a number or a string.
type ID struct {
	Num   int64
	Str   string
	IsStr bool
}

func (id ID) String() string {
	if id.IsStr {
		return strconv.Quote(id.Str)
	}
	return strconv.FormatInt(id.Num, 10)
}

func (id ID) MarshalJSON() ([]byte, error) {
	if id.IsStr {
		return json.Marshal(id.Str)
	}
	return json.Marshal(id.Num)
}

func (id *ID) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		id.IsStr = true
		return json.Unmarshal(b, &id.Str)
	}
	return json.Unmarshal(b, &id.Num)
}

// Message is any JSON-RPC message: a request has an ID and a Method, a notification only a Method, a response
// an ID and a Result or an Error.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *ID             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

func (m *Message) IsRequest() bool      { return m.ID != nil && m.Method != "" }
func (m *Message) IsNotification() bool { return m.ID == nil && m.Method != "" }
func (m *Message) IsResponse() bool     { return m.ID != nil && m.Method == "" }

// WriteFrame writes body with its header.
func WriteFrame(w io.Writer, body []byte) error {
	_, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

// ReadFrame reads one message's content.
func ReadFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for first := true; ; first = false {
		line, err := r.ReadString('\n')
		if err != nil {
			if first && line == "" && err == io.EOF {
				return nil, io.EOF
			}
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, fmt.Errorf("jsonrpc: bad header %q", line)
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("jsonrpc: bad Content-Length %q", value)
			}
			length = n
		case "content-type":
			if err := checkCharset(value); err != nil {
				return nil, err
			}
		}
	}
	if length < 0 {
		return nil, errors.New("jsonrpc: no Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

func checkCharset(ct string) error {
	for _, p := range strings.Split(ct, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
		if ok && strings.EqualFold(k, "charset") {
			if v = strings.ToLower(strings.Trim(v, `"`)); v != "utf-8" && v != "utf8" {
				return fmt.Errorf("jsonrpc: unsupported charset %q", v)
			}
		}
	}
	return nil
}

// Handlers answer what the other side sends. Both run on one goroutine, in the order messages arrive.
type Handlers struct {
	// Request answers a request; nil answers MethodNotFound. An *Error is sent as it is; any other error as
	// InternalError.
	Request func(ctx context.Context, method string, params json.RawMessage) (any, error)
	Notify  func(method string, params json.RawMessage)
}

// Conn is one JSON-RPC connection. It is safe for concurrent use. What it sends is queued and written in order by a
// goroutine of its own, so a send never waits on the other side reading.
type Conn struct {
	rwc io.ReadWriteCloser
	h   Handlers

	qmu   sync.Mutex
	queue []outgoing    // to write, in the order sent
	wake  chan struct{} // the writer has something to write

	mu      sync.Mutex
	next    int64
	pending map[int64]chan *Message
	err     error

	inbox chan *Message
	done  chan struct{}
	ctx   context.Context
	stop  context.CancelFunc
}

// NewConn starts reading from rwc.
func NewConn(rwc io.ReadWriteCloser, h Handlers) *Conn {
	ctx, stop := context.WithCancel(context.Background())
	c := &Conn{rwc: rwc, h: h, pending: map[int64]chan *Message{}, inbox: make(chan *Message, 64),
		done: make(chan struct{}), ctx: ctx, stop: stop, wake: make(chan struct{}, 1)}
	go c.read()
	go c.dispatch()
	go c.write()
	return c
}

// outgoing is a message queued to send, with its params still to marshal when they're given as a value.
type outgoing struct {
	m      *Message
	params any
}

// Done is closed once the connection has stopped reading.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err is why the connection stopped: ErrClosed after Close, or the read error.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *Conn) Close() error {
	c.fail(ErrClosed)
	return c.rwc.Close()
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
	defer close(c.done)
	defer close(c.inbox)
	r := bufio.NewReaderSize(c.rwc, 64<<10)
	for {
		body, err := ReadFrame(r)
		if err != nil {
			if err == io.EOF || errors.Is(err, io.ErrClosedPipe) {
				err = ErrClosed
			} else {
				err = fmt.Errorf("%w: %v", ErrClosed, err)
			}
			c.fail(err)
			return
		}
		m := new(Message)
		if err := json.Unmarshal(body, m); err != nil {
			continue
		}
		if m.IsResponse() {
			if m.ID.IsStr {
				continue
			}
			c.mu.Lock()
			ch := c.pending[m.ID.Num]
			delete(c.pending, m.ID.Num)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		c.inbox <- m
	}
}

// dispatch runs the handlers off the read loop, so a handler can make calls of its own.
func (c *Conn) dispatch() {
	var queue []*Message
	in := c.inbox
	work := make(chan *Message)
	go func() {
		for m := range work {
			c.handle(m)
		}
	}()
	defer close(work)
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
}

func (c *Conn) handle(m *Message) {
	if m.IsNotification() {
		if c.h.Notify != nil {
			c.h.Notify(m.Method, m.Params)
		}
		return
	}
	var res any
	var err error = ErrMethodNotFound
	if c.h.Request != nil && !strings.HasPrefix(m.Method, "$/") {
		res, err = c.h.Request(c.ctx, m.Method, m.Params)
	}
	resp := &Message{JSONRPC: "2.0", ID: m.ID}
	if err != nil {
		var e *Error
		if !errors.As(err, &e) {
			e = &Error{Code: InternalError, Message: err.Error()}
		}
		resp.Error = e
	} else if resp.Result, err = json.Marshal(res); err != nil {
		resp.Result, resp.Error = nil, &Error{Code: InternalError, Message: err.Error()}
	}
	c.send(resp, nil)
}

// send queues m, its params marshalled from params unless that's nil, behind everything sent before it.
func (c *Conn) send(m *Message, params any) error {
	if err := c.Err(); err != nil {
		return err
	}
	c.qmu.Lock()
	c.queue = append(c.queue, outgoing{m, params})
	c.qmu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
	return nil
}

// write writes what's queued, in order, until the connection fails.
func (c *Conn) write() {
	for {
		select {
		case <-c.wake:
		case <-c.ctx.Done():
			return
		}
		c.qmu.Lock()
		q := c.queue
		c.queue = nil
		c.qmu.Unlock()
		for _, o := range q {
			if o.params != nil {
				p, err := marshalParams(o.params)
				if err != nil {
					continue
				}
				o.m.Params = p
			}
			b, err := json.Marshal(o.m)
			if err != nil {
				continue
			}
			if c.Err() != nil {
				return
			}
			if err := WriteFrame(c.rwc, b); err != nil {
				c.fail(fmt.Errorf("%w: %v", ErrClosed, err))
				return
			}
		}
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

// Notify sends a notification. It's queued, its params marshalled when it's written.
func (c *Conn) Notify(method string, params any) error {
	return c.send(&Message{JSONRPC: "2.0", Method: method}, params)
}

// Call sends a request and decodes its result into result, if it isn't nil. If ctx ends first, Call sends
// $/cancelRequest and returns ctx's error.
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
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(&Message{JSONRPC: "2.0", ID: &ID{Num: id}, Method: method, Params: p}, nil); err != nil {
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
		c.Notify("$/cancelRequest", map[string]int64{"id": id})
		return nil, ctx.Err()
	}
}

func (c *Conn) forget(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

// IsNull reports whether raw is absent or JSON null.
func IsNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || string(raw) == "null"
}
