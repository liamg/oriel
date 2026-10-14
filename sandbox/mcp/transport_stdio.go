package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// DialStdio starts cmd (env and dir already set by the caller) and speaks MCP
// over its stdin/stdout. Stderr is left as the caller set it.
func DialStdio(ctx context.Context, cmd *exec.Cmd) (*Client, error) {
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{}
	t := &stdioTransport{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: map[string]chan *message{},
		done:    make(chan struct{}),
		handle:  c.handle,
	}
	c.t = t
	go t.read(stdout)
	if err := c.initialize(ctx); err != nil {
		_ = t.close()
		return nil, err
	}
	return c, nil
}

type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	handle func(*message)

	wmu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan *message
	err     error
	done    chan struct{}
	closed  sync.Once
}

func (t *stdioTransport) read(r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var m message
			if json.Unmarshal(line, &m) == nil {
				if m.isResponse() {
					t.mu.Lock()
					ch := t.pending[idKey(m.ID)]
					delete(t.pending, idKey(m.ID))
					t.mu.Unlock()
					if ch != nil {
						ch <- &m
					}
				} else {
					t.handle(&m)
				}
			}
		}
		if err != nil {
			break
		}
	}
	werr := t.cmd.Wait()
	t.mu.Lock()
	t.err = errors.New("mcp: server exited")
	if werr != nil {
		t.err = fmt.Errorf("mcp: server exited: %w", werr)
	}
	t.pending = map[string]chan *message{}
	t.mu.Unlock()
	close(t.done)
}

func (t *stdioTransport) write(m *message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	t.wmu.Lock()
	defer t.wmu.Unlock()
	_, err = t.stdin.Write(b)
	return err
}

func (t *stdioTransport) request(ctx context.Context, m *message) (*message, error) {
	key := idKey(m.ID)
	ch := make(chan *message, 1)
	t.mu.Lock()
	if t.err != nil {
		err := t.err
		t.mu.Unlock()
		return nil, err
	}
	t.pending[key] = ch
	t.mu.Unlock()

	if err := t.write(m); err != nil {
		t.forget(key)
		return nil, err
	}
	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		t.forget(key)
		params, _ := json.Marshal(map[string]any{"requestId": m.ID, "reason": ctx.Err().Error()})
		_ = t.write(&message{JSONRPC: "2.0", Method: "notifications/cancelled", Params: params})
		return nil, ctx.Err()
	case <-t.done:
		select {
		case resp := <-ch:
			return resp, nil
		default:
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		return nil, t.err
	}
}

func (t *stdioTransport) forget(key string) {
	t.mu.Lock()
	delete(t.pending, key)
	t.mu.Unlock()
}

func (t *stdioTransport) send(_ context.Context, m *message) error {
	return t.write(m)
}

// close closes the server's stdin and waits briefly for it to exit before
// killing it.
func (t *stdioTransport) close() error {
	t.closed.Do(func() {
		_ = t.stdin.Close()
		select {
		case <-t.done:
		case <-time.After(5 * time.Second):
			_ = t.cmd.Process.Kill()
			_ = t.stdout.Close()
			<-t.done
		}
	})
	return nil
}
