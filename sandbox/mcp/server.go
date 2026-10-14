package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
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
	conn *serverConn
}

type serverConn struct {
	wmu sync.Mutex
	w   io.Writer

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func (c *serverConn) write(m *message) {
	m.JSONRPC = "2.0"
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	b = append(b, '\n')
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, _ = c.w.Write(b)
}

// Serve runs the server over r/w (newline-delimited JSON-RPC) until r hits EOF
// or ctx ends. Requests are handled concurrently; in-flight calls are
// cancelled when Serve returns.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()

	c := &serverConn{w: w, cancels: map[string]context.CancelFunc{}}
	s.mu.Lock()
	s.conn = c
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.conn == c {
			s.conn = nil
		}
		s.mu.Unlock()
	}()

	lines := make(chan []byte)
	errc := make(chan error, 1)
	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				select {
				case lines <- line:
				case <-ctx.Done():
					return
				}
			}
			if err != nil {
				errc <- err
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		case line := <-lines:
			s.dispatch(ctx, c, line, &wg)
		}
	}
}

// NotifyToolsChanged tells the connected client the tool list changed. It is
// a no-op when not serving.
func (s *Server) NotifyToolsChanged() {
	s.mu.Lock()
	c := s.conn
	s.mu.Unlock()
	if c != nil {
		c.write(&message{Method: "notifications/tools/list_changed"})
	}
}

func (s *Server) dispatch(ctx context.Context, c *serverConn, line []byte, wg *sync.WaitGroup) {
	var m message
	if err := json.Unmarshal(line, &m); err != nil {
		c.write(&message{ID: json.RawMessage("null"), Error: &Error{Code: CodeParseError, Message: "parse error"}})
		return
	}
	if m.Method == "" {
		return
	}
	if m.ID == nil {
		if m.Method == "notifications/cancelled" {
			var p struct {
				RequestID json.RawMessage `json:"requestId"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.RequestID != nil {
				c.mu.Lock()
				if f := c.cancels[idKey(p.RequestID)]; f != nil {
					f()
				}
				c.mu.Unlock()
			}
		}
		return
	}

	key := idKey(m.ID)
	rctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancels[key] = cancel
	c.mu.Unlock()
	wg.Go(func() {
		defer func() {
			c.mu.Lock()
			delete(c.cancels, key)
			c.mu.Unlock()
			cancel()
		}()
		result, err := s.handle(rctx, &m)
		resp := &message{ID: m.ID}
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				e = &Error{Code: CodeInternalError, Message: err.Error()}
			}
			resp.Error = e
		} else {
			b, err := json.Marshal(result)
			if err != nil {
				resp.Error = &Error{Code: CodeInternalError, Message: err.Error()}
			} else {
				resp.Result = b
			}
		}
		c.write(resp)
	})
}

func (s *Server) handle(ctx context.Context, m *message) (any, error) {
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
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
		if err := json.Unmarshal(m.Params, &p); err != nil || p.Name == "" {
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
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + m.Method}
}
