package mcp

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"sync"

	"github.com/liamg/oriel/jsonrpc"
)

// Handler supplies a server's tools.
type Handler interface {
	Tools(ctx context.Context) ([]Tool, error)
	Call(ctx context.Context, name string, args json.RawMessage) (CallResult, error)
}

// Server serves a Handler over the MCP stdio transport.
type Server struct {
	Name         string
	Version      string
	Handler      Handler
	Instructions string

	mu   sync.Mutex
	conn *jsonrpc.Conn
}

// Serve runs the server over r/w (newline-delimited JSON-RPC) until r hits EOF
// or ctx ends. Requests are handled concurrently; in-flight calls are
// cancelled when Serve returns. It doesn't close r or w.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	var (
		mu      sync.Mutex
		stopped bool
		wg      sync.WaitGroup
	)
	request := func(req *jsonrpc.Request) {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			req.Reply(nil, ctx.Err())
			return
		}
		wg.Go(func() {
			hctx, stop := context.WithCancel(ctx)
			defer context.AfterFunc(req.Context(), stop)()
			defer stop()
			req.Reply(s.handle(hctx, req.Method, req.Params))
		})
	}
	conn := jsonrpc.NewConn(struct{ io.Reader }{r}, struct{ io.Writer }{w}, jsonrpc.Handlers{Request: request},
		jsonrpc.Options{Version: "2.0", Cancel: cancel, ParseErrors: true})
	s.mu.Lock()
	s.conn = conn
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conn == conn {
			s.conn = nil
		}
		s.mu.Unlock()
	}()

	var err error
	select {
	case <-conn.Done():
		if e := conn.Err(); e != jsonrpc.ErrClosed {
			err = e
		}
	case <-ctx.Done():
		err = ctx.Err()
		conn.Close()
	}
	mu.Lock()
	stopped = true
	mu.Unlock()
	wg.Wait()
	conn.Close()
	return err
}

// NotifyToolsChanged tells the connected client the tool list changed. It is
// a no-op when not serving.
func (s *Server) NotifyToolsChanged() {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.Notify("notifications/tools/list_changed", nil)
	}
}

func (s *Server) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(params, &p)
		v := ProtocolVersion
		if slices.Contains(knownVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return initializeResult{
			ProtocolVersion: v,
			Capabilities:    json.RawMessage(`{"tools":{"listChanged":true}}`),
			ServerInfo:      ServerInfo{Name: s.Name, Version: s.Version},
			Instructions:    s.Instructions,
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		tools, err := s.Handler.Tools(ctx)
		if err != nil {
			return nil, err
		}
		if tools == nil {
			tools = []Tool{}
		}
		for i := range tools {
			if len(tools[i].InputSchema) == 0 {
				tools[i].InputSchema = json.RawMessage(`{"type":"object"}`)
			}
		}
		return listToolsResult{Tools: tools}, nil
	case "tools/call":
		var p callParams
		if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
			return nil, &Error{Code: CodeInvalidParams, Message: "invalid params"}
		}
		res, err := s.Handler.Call(ctx, p.Name, p.Arguments)
		if err != nil {
			res = TextResult(err.Error(), true)
		}
		if res.Content == nil {
			res.Content = []json.RawMessage{}
		}
		return res, nil
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}
