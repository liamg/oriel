package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

type fakeHandler struct {
	release chan struct{}
}

func (h *fakeHandler) Tools(context.Context) ([]Tool, error) {
	return []Tool{
		{Name: "echo", Description: "echoes", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "fail"},
		{Name: "slow"},
	}, nil
}

func (h *fakeHandler) Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error) {
	switch name {
	case "echo":
		var a struct{ Text string }
		_ = json.Unmarshal(args, &a)
		return TextResult(a.Text, false), nil
	case "slow":
		select {
		case <-h.release:
			return TextResult("done", false), nil
		case <-ctx.Done():
			return CallResult{}, ctx.Err()
		}
	}
	return CallResult{}, errors.New("boom")
}

type pipeConn struct {
	t   *testing.T
	in  *io.PipeWriter
	out *bufio.Reader

	mu      sync.Mutex
	waiting map[string]chan *message
	notes   chan *message
}

func servePipe(t *testing.T, s *Server) *pipeConn {
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, inR, outW) }()
	c := &pipeConn{t: t, in: inW, out: bufio.NewReader(outR), waiting: map[string]chan *message{}, notes: make(chan *message, 10)}
	go func() {
		for {
			line, err := c.out.ReadBytes('\n')
			if err != nil {
				return
			}
			var m message
			if err := json.Unmarshal(line, &m); err != nil {
				t.Errorf("bad line %q", line)
				return
			}
			if m.ID == nil {
				c.notes <- &m
				continue
			}
			c.mu.Lock()
			ch := c.waiting[idKey(m.ID)]
			c.mu.Unlock()
			if ch == nil {
				t.Errorf("unexpected response %s", line)
				continue
			}
			ch <- &m
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return on EOF")
		}
		cancel()
		_ = outR.Close()
	})
	return c
}

func (c *pipeConn) start(id, method, params string) chan *message {
	ch := make(chan *message, 1)
	c.mu.Lock()
	c.waiting[idKey(json.RawMessage(id))] = ch
	c.mu.Unlock()
	line := `{"jsonrpc":"2.0","id":` + id + `,"method":"` + method + `"`
	if params != "" {
		line += `,"params":` + params
	}
	if _, err := c.in.Write([]byte(line + "}\n")); err != nil {
		c.t.Fatal(err)
	}
	return ch
}

func (c *pipeConn) wait(ch chan *message) *message {
	c.t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		c.t.Fatal("timed out waiting for response")
		return nil
	}
}

func (c *pipeConn) call(id, method, params string) *message {
	c.t.Helper()
	return c.wait(c.start(id, method, params))
}

func TestServer(t *testing.T) {
	h := &fakeHandler{release: make(chan struct{})}
	s := &Server{Name: "test", Version: "1.0", Handler: h, Instructions: "be nice"}
	c := servePipe(t, s)

	m := c.call("1", "initialize", `{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}`)
	var ir struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools struct {
				ListChanged bool `json:"listChanged"`
			} `json:"tools"`
		} `json:"capabilities"`
		ServerInfo   ServerInfo `json:"serverInfo"`
		Instructions string     `json:"instructions"`
	}
	if err := json.Unmarshal(m.Result, &ir); err != nil {
		t.Fatal(err)
	}
	if ir.ProtocolVersion != ProtocolVersion || !ir.Capabilities.Tools.ListChanged || ir.ServerInfo.Name != "test" || ir.Instructions != "be nice" {
		t.Fatalf("initialize result %s", m.Result)
	}
	if _, err := c.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		t.Fatal(err)
	}

	m = c.call(`"list"`, "tools/list", "")
	if string(m.ID) != `"list"` {
		t.Fatalf("string id not echoed: %s", m.ID)
	}
	var lr listToolsResult
	if err := json.Unmarshal(m.Result, &lr); err != nil || len(lr.Tools) != 3 {
		t.Fatalf("tools/list: %s", m.Result)
	}
	if string(lr.Tools[1].InputSchema) != `{"type":"object"}` {
		t.Fatalf("default schema missing: %s", lr.Tools[1].InputSchema)
	}

	m = c.call("3", "tools/call", `{"name":"echo","arguments":{"Text":"hi"}}`)
	var cr CallResult
	if err := json.Unmarshal(m.Result, &cr); err != nil || cr.IsError || cr.Text() != "hi" {
		t.Fatalf("echo: %s", m.Result)
	}

	m = c.call("4", "tools/call", `{"name":"fail"}`)
	cr = CallResult{}
	if err := json.Unmarshal(m.Result, &cr); err != nil || !cr.IsError || cr.Text() != "boom" || m.Error != nil {
		t.Fatalf("fail: %s %v", m.Result, m.Error)
	}

	m = c.call("5", "nope", "")
	if m.Error == nil || m.Error.Code != CodeMethodNotFound {
		t.Fatalf("unknown method: %+v", m)
	}

	slow := c.start("6", "tools/call", `{"name":"slow"}`)
	if m := c.call("7", "ping", ""); m.Error != nil || string(m.Result) != "{}" {
		t.Fatalf("ping: %+v", m)
	}
	select {
	case <-slow:
		t.Fatal("slow call finished early")
	default:
	}
	close(h.release)
	m = c.wait(slow)
	cr = CallResult{}
	if err := json.Unmarshal(m.Result, &cr); err != nil || cr.Text() != "done" {
		t.Fatalf("slow: %s", m.Result)
	}

	s.NotifyToolsChanged()
	select {
	case n := <-c.notes:
		if n.Method != "notifications/tools/list_changed" {
			t.Fatalf("notification %q", n.Method)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no list_changed notification")
	}
}

func TestServerCancel(t *testing.T) {
	h := &fakeHandler{release: make(chan struct{})}
	c := servePipe(t, &Server{Name: "t", Handler: h})
	slow := c.start("1", "tools/call", `{"name":"slow"}`)
	if _, err := c.in.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	m := c.wait(slow)
	var cr CallResult
	if err := json.Unmarshal(m.Result, &cr); err != nil || !cr.IsError {
		t.Fatalf("cancelled call: %s", m.Result)
	}
}

func TestNotifyToolsChangedNotServing(t *testing.T) {
	(&Server{}).NotifyToolsChanged()
}
