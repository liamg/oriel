package nvim

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// client is a synchronous msgpack-RPC client: one request in flight at a time.
type client struct {
	w       *bufio.Writer
	replies chan reply
	nextID  int64
	timeout time.Duration

	mu  sync.Mutex
	err error // set once the read loop stops
}

type reply struct {
	id     int64
	err    any
	result any
}

func newClient(r io.Reader, w io.Writer, timeout time.Duration) *client {
	c := &client{w: bufio.NewWriter(w), replies: make(chan reply, 1), timeout: timeout}
	go c.readLoop(bufio.NewReader(r))
	return c
}

func (c *client) readLoop(r *bufio.Reader) {
	for {
		v, err := decode(r)
		if err != nil {
			c.mu.Lock()
			c.err = err
			c.mu.Unlock()
			close(c.replies)
			return
		}
		msg, ok := v.([]any)
		if !ok || len(msg) == 0 {
			continue
		}
		// Notifications ([2, method, params]) and requests from nvim are ignored.
		if typ, _ := msg[0].(int64); typ == 1 && len(msg) == 4 {
			id, _ := msg[1].(int64)
			c.replies <- reply{id: id, err: msg[2], result: msg[3]}
		}
	}
}

// call sends a request and waits for its response.
func (c *client) call(method string, args ...any) (any, error) {
	return c.callTimeout(c.timeout, method, args...)
}

func (c *client) callTimeout(timeout time.Duration, method string, args ...any) (any, error) {
	if args == nil {
		args = []any{}
	}
	c.nextID++
	id := c.nextID
	if err := encode(c.w, []any{0, id, method, args}); err != nil {
		return nil, err
	}
	if err := c.w.Flush(); err != nil {
		return nil, err
	}
	deadline := time.After(timeout)
	for {
		select {
		case rep, ok := <-c.replies:
			if !ok {
				c.mu.Lock()
				defer c.mu.Unlock()
				return nil, fmt.Errorf("nvim: connection closed: %w", c.err)
			}
			if rep.id != id {
				continue
			}
			if rep.err != nil {
				return nil, fmt.Errorf("nvim: %s: %v", method, rpcError(rep.err))
			}
			return rep.result, nil
		case <-deadline:
			return nil, errors.New("nvim: " + method + ": timed out (is nvim waiting for input?)")
		}
	}
}

// rpcError extracts the message from nvim's [type, message] error value.
func rpcError(v any) any {
	if e, ok := v.([]any); ok && len(e) == 2 {
		return e[1]
	}
	return v
}
