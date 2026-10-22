package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"time"

	"github.com/liamg/oriel/jsonrpc"
)

// DialStdio starts cmd (env and dir already set by the caller) and speaks MCP
// over its stdin/stdout. Stderr is left as the caller set it.
func DialStdio(ctx context.Context, cmd *exec.Cmd) (*Client, error) {
	p, err := jsonrpc.StartStdio(cmd, cmd.Stderr)
	if err != nil {
		return nil, err
	}
	c := &Client{}
	t := &stdioTransport{proc: p}
	t.conn = jsonrpc.NewConn(p, p, jsonrpc.Handlers{
		Request: jsonrpc.Answer(func(_ context.Context, method string, _ json.RawMessage) (any, error) {
			return c.answer(method)
		}),
		Notify: func(method string, _ json.RawMessage) { c.notified(method) },
	}, jsonrpc.Options{Version: "2.0", Cancel: cancel})
	c.t = t
	if err := c.initialize(ctx); err != nil {
		_ = t.close()
		return nil, err
	}
	return c, nil
}

type stdioTransport struct {
	proc *jsonrpc.Process
	conn *jsonrpc.Conn
}

func (t *stdioTransport) request(ctx context.Context, m *message) (*message, error) {
	raw, err := t.conn.CallRaw(ctx, m.Method, m.Params)
	var e *Error
	if errors.As(err, &e) {
		return &message{ID: m.ID, Error: e}, nil
	}
	if err != nil {
		return nil, err
	}
	return &message{ID: m.ID, Result: raw}, nil
}

func (t *stdioTransport) send(_ context.Context, m *message) error {
	return t.conn.Notify(m.Method, m.Params)
}

// close closes the server's stdin and stdout and waits briefly for it to exit
// before killing it.
func (t *stdioTransport) close() error {
	_ = t.conn.Close()
	select {
	case <-t.proc.Exited():
	case <-time.After(5 * time.Second):
		t.proc.Stop()
	}
	return nil
}
