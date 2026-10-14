package jsonrpc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ref: lsp/specification.md#headerPart
// quote: "Each header field is terminated by '\\r\\n'. Considering the last header field and the overall header itself are each terminated with '\\r\\n', and that at least one header is mandatory, this means that two '\\r\\n' sequences always immediately precede the content part of a message."
func TestWriteFrame(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, []byte(`{"a":"é"}`)); err != nil {
		t.Fatal(err)
	}
	// ref: lsp/specification.md#headerPart
	// quote: "Content-Length    | number      | The length of the content part in bytes. This header is required."
	if got, want := b.String(), "Content-Length: 10\r\n\r\n{\"a\":\"é\"}"; got != want {
		t.Errorf("frame = %q, want %q", got, want)
	}
}

func TestReadFrame(t *testing.T) {
	// ref: lsp/specification.md#contentPart
	// quote: "For backwards compatibility it is highly recommended that a client and a server treat the string `utf8` as `utf-8`."
	in := "Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=utf8\r\n\r\n{}" +
		"content-length: 4\r\n\r\nnull"
	r := bufio.NewReader(strings.NewReader(in))
	for _, want := range []string{"{}", "null"} {
		got, err := ReadFrame(r)
		if err != nil || string(got) != want {
			t.Fatalf("ReadFrame = %q, %v; want %q", got, err, want)
		}
	}
	if _, err := ReadFrame(r); err != io.EOF {
		t.Errorf("at the end ReadFrame err = %v, want EOF", err)
	}
}

func TestReadFrameErrors(t *testing.T) {
	for _, in := range []string{
		"Content-Type: x\r\n\r\n{}", // no length
		"Content-Length: x\r\n\r\n{}",
		"Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=latin1\r\n\r\n{}",
		"Content-Length: 9\r\n\r\n{}",
	} {
		if _, err := ReadFrame(bufio.NewReader(strings.NewReader(in))); err == nil {
			t.Errorf("ReadFrame(%q) succeeded", in)
		}
	}
}

// peer is the other end of a Conn, speaking raw frames.
type peer struct {
	t  *testing.T
	rw net.Conn
	r  *bufio.Reader
}

func newPair(t *testing.T, h Handlers) (*Conn, *peer) {
	a, b := net.Pipe()
	c := NewConn(a, h)
	t.Cleanup(func() { c.Close() })
	return c, &peer{t: t, rw: b, r: bufio.NewReader(b)}
}

func (p *peer) read() map[string]any {
	p.t.Helper()
	p.rw.SetReadDeadline(time.Now().Add(5 * time.Second))
	b, err := ReadFrame(p.r)
	if err != nil {
		p.t.Fatalf("peer read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		p.t.Fatal(err)
	}
	return m
}

func (p *peer) write(s string) {
	p.t.Helper()
	if err := WriteFrame(p.rw, []byte(s)); err != nil {
		p.t.Fatal(err)
	}
}

func TestCall(t *testing.T) {
	c, p := newPair(t, Handlers{})
	type res struct {
		v   string
		err error
	}
	done := make(chan res)
	go func() {
		var v string
		err := c.Call(context.Background(), "textDocument/hover", map[string]int{"x": 1}, &v)
		done <- res{v, err}
	}()
	m := p.read()
	// ref: lsp/specification.md#abstractMessage
	// quote: "The language server protocol always uses \"2.0\" as the `jsonrpc` version."
	if m["jsonrpc"] != "2.0" || m["method"] != "textDocument/hover" || m["id"] == nil {
		t.Fatalf("request = %v", m)
	}
	p.write(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"result":"ok"}`, m["id"]))
	if r := <-done; r.err != nil || r.v != "ok" {
		t.Errorf("Call = %q, %v", r.v, r.err)
	}
}

// ref: lsp/specification.md#responseMessage
// quote: "The error object in case a request fails."
func TestCallError(t *testing.T) {
	c, p := newPair(t, Handlers{})
	done := make(chan error)
	go func() { done <- c.Call(context.Background(), "x", nil, nil) }()
	m := p.read()
	p.write(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"error":{"code":-32803,"message":"no"}}`, m["id"]))
	var e *Error
	if err := <-done; !errors.As(err, &e) || e.Code != RequestFailed || e.Message != "no" {
		t.Errorf("Call err = %v, want RequestFailed", err)
	}
}

// ref: lsp/specification.md#cancelRequest
// quote: "To cancel a request, a notification message with the following properties is sent:"
func TestCallCancel(t *testing.T) {
	c, p := newPair(t, Handlers{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- c.Call(ctx, "slow", nil, nil) }()
	m := p.read()
	cancel()
	n := p.read()
	if n["method"] != "$/cancelRequest" || n["id"] != nil {
		t.Fatalf("after cancel = %v", n)
	}
	if params := n["params"].(map[string]any); params["id"] != m["id"] {
		t.Errorf("cancel id = %v, want %v", params["id"], m["id"])
	}
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Call err = %v", err)
	}
	// The server still answers; the late response is dropped.
	p.write(fmt.Sprintf(`{"jsonrpc":"2.0","id":%v,"error":{"code":-32800,"message":"cancelled"}}`, m["id"]))
}

// ref: lsp/specification.md#notificationMessage
// quote: "A processed notification message must not send a response back."
func TestNotify(t *testing.T) {
	c, p := newPair(t, Handlers{})
	go c.Notify("initialized", struct{}{})
	m := p.read()
	if _, ok := m["id"]; ok || m["method"] != "initialized" {
		t.Errorf("notification = %v", m)
	}
}

func TestIncoming(t *testing.T) {
	var mu sync.Mutex
	var notes []string
	_, p := newPair(t, Handlers{
		Request: func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case "workspace/configuration":
				return []any{nil}, nil
			case "fail":
				return nil, &Error{Code: InvalidParams, Message: "bad"}
			}
			return nil, ErrMethodNotFound
		},
		Notify: func(method string, params json.RawMessage) {
			mu.Lock()
			notes = append(notes, method+" "+string(params))
			mu.Unlock()
		},
	})
	p.write(`{"jsonrpc":"2.0","method":"$/progress","params":{"token":1}}`)
	p.write(`{"jsonrpc":"2.0","id":"a","method":"workspace/configuration","params":{}}`)
	if m := p.read(); m["id"] != "a" || fmt.Sprint(m["result"]) != "[<nil>]" {
		t.Errorf("response = %v", m)
	}
	// ref: lsp/specification.md#responseMessage
	// quote: "The result property of the ResponseMessage should be set to `null` in this case to signal a successful request."
	p.write(`{"jsonrpc":"2.0","id":7,"method":"fail"}`)
	if m := p.read(); m["id"] != float64(7) || m["error"].(map[string]any)["code"] != float64(InvalidParams) {
		t.Errorf("error response = %v", m)
	}
	// ref: lsp/specification.md#dollarRequests
	// quote: "If a server or client receives a request starting with '\\$/' it must error the request with error code `MethodNotFound` (e.g. `-32601`)."
	p.write(`{"jsonrpc":"2.0","id":8,"method":"$/unknown"}`)
	if m := p.read(); m["error"].(map[string]any)["code"] != float64(MethodNotFound) {
		t.Errorf("$/ request = %v", m)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 1 || notes[0] != `$/progress {"token":1}` {
		t.Errorf("notifications = %q", notes)
	}
}

func TestNullResult(t *testing.T) {
	_, p := newPair(t, Handlers{Request: func(context.Context, string, json.RawMessage) (any, error) { return nil, nil }})
	p.write(`{"jsonrpc":"2.0","id":1,"method":"client/registerCapability","params":{}}`)
	m := p.read()
	if r, ok := m["result"]; !ok || r != nil {
		t.Errorf("response = %v, want result null", m)
	}
}

func TestClosedConn(t *testing.T) {
	c, p := newPair(t, Handlers{})
	done := make(chan error)
	go func() { done <- c.Call(context.Background(), "x", nil, nil) }()
	p.read()
	p.rw.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Errorf("Call on a closed conn = %v", err)
	}
	select {
	case <-c.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Done not closed")
	}
	if err := c.Call(context.Background(), "y", nil, nil); !errors.Is(err, ErrClosed) {
		t.Errorf("Call after close = %v", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	c, p := newPair(t, Handlers{})
	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var got int
			if err := c.Call(context.Background(), "echo", i, &got); err != nil || got != i {
				errs <- fmt.Errorf("call %d = %d, %v", i, got, err)
			}
		}()
	}
	go func() {
		for range n {
			m := p.read()
			b, _ := json.Marshal(m["id"])
			p.write(fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":%v}`, b, m["params"]))
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
