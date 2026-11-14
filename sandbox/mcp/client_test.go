package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("MCP_TEST_SERVER") == "1" {
		runTestServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type stdioHandler struct {
	s     *Server
	extra atomic.Bool
}

func (h *stdioHandler) Tools(context.Context) ([]Tool, error) {
	tools := []Tool{{Name: "echo"}, {Name: "change"}}
	if h.extra.Load() {
		tools = append(tools, Tool{Name: "extra"})
	}
	return tools, nil
}

func (h *stdioHandler) Call(_ context.Context, name string, args json.RawMessage) (CallResult, error) {
	switch name {
	case "echo":
		return TextResult(string(args), false), nil
	case "change":
		h.extra.Store(true)
		h.s.NotifyToolsChanged()
		return TextResult("ok", false), nil
	}
	return CallResult{}, fmt.Errorf("unknown tool %q", name)
}

func runTestServer() {
	h := &stdioHandler{}
	h.s = &Server{Name: "stdio-test", Version: "1", Handler: h}
	_ = h.s.Serve(context.Background(), os.Stdin, os.Stdout)
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestClientStdio(t *testing.T) {
	ctx := context.Background()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "MCP_TEST_SERVER=1")
	cmd.Stderr = os.Stderr
	c, err := DialStdio(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if c.ServerInfo().Name != "stdio-test" {
		t.Fatalf("server info %+v", c.ServerInfo())
	}

	changed := make(chan struct{}, 1)
	c.OnToolsChanged(func() { changed <- struct{}{} })

	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 2 {
		t.Fatalf("ListTools: %v %v", tools, err)
	}
	res, err := c.CallTool(ctx, "echo", json.RawMessage(`{"a":1}`))
	if err != nil || res.IsError || res.Text() != `{"a":1}` {
		t.Fatalf("echo: %+v %v", res, err)
	}
	res, err = c.CallTool(ctx, "missing", nil)
	if err != nil || !res.IsError {
		t.Fatalf("missing: %+v %v", res, err)
	}
	if _, err := c.CallTool(ctx, "change", nil); err != nil {
		t.Fatal(err)
	}
	waitSignal(t, changed, "list_changed")
	if tools, _ := c.ListTools(ctx); len(tools) != 3 {
		t.Fatalf("tools after change: %v", tools)
	}

	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListTools(ctx); err == nil {
		t.Fatal("expected error after close")
	}
}

// httpServer is a fake Streamable HTTP MCP server.
type httpServer struct {
	t   *testing.T
	sse bool

	mu        sync.Mutex
	sessions  map[string]bool
	inits     int
	deleted   []string
	push      chan struct{}
	listening chan struct{}
	listenOne sync.Once
	next      int
}

func newHTTPServer(t *testing.T, sse bool) (*httpServer, *httptest.Server) {
	s := &httpServer{t: t, sse: sse, sessions: map[string]bool{}, push: make(chan struct{}, 1), listening: make(chan struct{})}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	return s, srv
}

func (s *httpServer) expire() {
	s.mu.Lock()
	s.sessions = map[string]bool{}
	s.mu.Unlock()
}

func (s *httpServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sid := r.Header.Get("Mcp-Session-Id")
	s.mu.Lock()
	valid := s.sessions[sid]
	s.mu.Unlock()

	switch r.Method {
	case http.MethodGet:
		if !s.sse {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if !valid {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		s.listenOne.Do(func() { close(s.listening) })
		for {
			select {
			case <-s.push:
				_, _ = fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
				w.(http.Flusher).Flush()
			case <-r.Context().Done():
				return
			}
		}
	case http.MethodDelete:
		s.mu.Lock()
		s.deleted = append(s.deleted, sid)
		delete(s.sessions, sid)
		s.mu.Unlock()
		return
	}

	if !strings.Contains(r.Header.Get("Accept"), "text/event-stream") || !strings.Contains(r.Header.Get("Accept"), "application/json") {
		s.t.Errorf("bad Accept %q", r.Header.Get("Accept"))
	}
	var m message
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if m.Method == "initialize" {
		s.mu.Lock()
		s.inits++
		s.next++
		sid = fmt.Sprintf("sess-%d", s.next)
		s.sessions[sid] = true
		s.mu.Unlock()
		w.Header().Set("Mcp-Session-Id", sid)
		s.reply(w, &m, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      ServerInfo{Name: "http-test", Version: "1"},
		}, false)
		return
	}
	if sid == "" {
		http.Error(w, "missing session", http.StatusBadRequest)
		return
	}
	if !valid {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if r.Header.Get("MCP-Protocol-Version") != ProtocolVersion {
		s.t.Errorf("missing protocol version header on %s", m.Method)
	}
	if m.ID == nil || m.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	switch m.Method {
	case "tools/list":
		var p struct{ Cursor string }
		_ = json.Unmarshal(m.Params, &p)
		if p.Cursor == "" {
			s.reply(w, &m, listToolsResult{Tools: []Tool{{Name: "a", InputSchema: json.RawMessage(`{}`)}}, NextCursor: "p2"}, false)
		} else {
			s.reply(w, &m, listToolsResult{Tools: []Tool{{Name: "b", InputSchema: json.RawMessage(`{}`)}}}, false)
		}
	case "tools/call":
		var p callParams
		_ = json.Unmarshal(m.Params, &p)
		s.reply(w, &m, TextResult(p.Name+":"+string(p.Arguments), false), true)
	default:
		b, _ := json.Marshal(message{JSONRPC: "2.0", ID: m.ID, Error: &Error{Code: CodeMethodNotFound, Message: "nope"}})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}
}

func (s *httpServer) reply(w http.ResponseWriter, req *message, result any, notify bool) {
	res, _ := json.Marshal(result)
	b, _ := json.Marshal(message{JSONRPC: "2.0", ID: req.ID, Result: res})
	if !s.sse {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if notify {
		_, _ = fmt.Fprint(w, ": comment\n\nevent: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/tools/list_changed\"}\n\n")
	}
	_, _ = fmt.Fprintf(w, "id: 1\ndata: %s\n\n", b)
}

func testHTTPClient(t *testing.T, sse bool) {
	ctx := context.Background()
	s, srv := newHTTPServer(t, sse)
	hdr := http.Header{"X-Static": {"yes"}}
	c, err := DialHTTP(ctx, srv.URL, hdr)
	if err != nil {
		t.Fatal(err)
	}
	if c.ServerInfo().Name != "http-test" {
		t.Fatalf("server info %+v", c.ServerInfo())
	}
	changed := make(chan struct{}, 10)
	c.OnToolsChanged(func() { changed <- struct{}{} })

	tools, err := c.ListTools(ctx)
	if err != nil || len(tools) != 2 || tools[0].Name != "a" || tools[1].Name != "b" {
		t.Fatalf("ListTools: %+v %v", tools, err)
	}
	res, err := c.CallTool(ctx, "x", json.RawMessage(`{"k":"v"}`))
	if err != nil || res.Text() != `x:{"k":"v"}` {
		t.Fatalf("CallTool: %+v %v", res, err)
	}
	if sse {
		waitSignal(t, changed, "inline list_changed")
		waitSignal(t, s.listening, "GET stream")
		s.push <- struct{}{}
		waitSignal(t, changed, "list_changed over GET stream")
	}

	s.expire()
	res, err = c.CallTool(ctx, "y", nil)
	if err != nil || res.Text() != "y:{}" {
		t.Fatalf("CallTool after expiry: %+v %v", res, err)
	}
	s.mu.Lock()
	inits := s.inits
	s.mu.Unlock()
	if inits != 2 {
		t.Fatalf("inits = %d, want 2", inits)
	}

	if _, err := c.CallTool(ctx, "z", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.deleted) != 1 || s.deleted[0] != "sess-2" {
		t.Fatalf("deleted = %v", s.deleted)
	}
}

func TestClientHTTPJSON(t *testing.T) { testHTTPClient(t, false) }
func TestClientHTTPSSE(t *testing.T)  { testHTTPClient(t, true) }

func TestClientHTTPError(t *testing.T) {
	_, srv := newHTTPServer(t, false)
	c, err := DialHTTP(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	var e *Error
	err = c.call(context.Background(), "nope", nil, nil)
	if !errors.As(err, &e) || e.Code != CodeMethodNotFound {
		t.Fatalf("err = %v", err)
	}
}
