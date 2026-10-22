// Package mcptools serves a session's Oriel tools to its agent as the MCP server oriel: a unix socket answering
// tools/list and tools/call, which `oriel hook mcp` (mcpstdio) bridges to the agent over stdio.
package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/jsonrpc"
)

// SockEnv names the socket `oriel hook mcp` (mcpstdio) dials.
const SockEnv = agent.HookSocketEnv

// Server answers tools/list and tools/call for `oriel hook mcp` on a unix socket.
type Server struct {
	dir   string
	path  string
	ln    net.Listener
	tools []agent.Tool
	ctx   context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup

	mu    sync.Mutex
	conns []*jsonrpc.Conn
}

// sockDirMax is how long the socket's directory may be: a unix socket's path has to fit in about 100 bytes.
const sockDirMax = 80

// Serve listens in a fresh short directory: the temp directory's, or /tmp's when that's too long.
func Serve(tools []agent.Tool) (*Server, error) {
	base := os.TempDir()
	if len(base) > sockDirMax {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "oriel-mcp-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "tools.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{dir: dir, path: path, ln: ln, tools: tools, ctx: ctx, stop: stop}
	s.wg.Go(s.accept)
	return s, nil
}

// Path is the socket's path, for SockEnv.
func (s *Server) Path() string { return s.path }

func (s *Server) accept() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		c := jsonrpc.NewConn(nc, nc, jsonrpc.Handlers{Request: s.handle}, jsonrpc.Options{Version: "2.0"})
		s.mu.Lock()
		s.conns = append(s.conns, c)
		s.mu.Unlock()
	}
}

func (s *Server) handle(r *jsonrpc.Request) {
	switch r.Method {
	case "tools/list":
		list := []map[string]any{}
		for _, t := range s.tools {
			schema := t.Schema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			m := map[string]any{"name": t.Name, "description": t.Description, "inputSchema": schema}
			if t.ReadOnly {
				m["annotations"] = map[string]any{"readOnlyHint": true}
			}
			list = append(list, m)
		}
		r.Reply(map[string]any{"tools": list}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(r.Params, &p); err != nil || p.Name == "" {
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "invalid params"})
			return
		}
		i := slices.IndexFunc(s.tools, func(t agent.Tool) bool { return t.Name == p.Name })
		if i < 0 || s.tools[i].Call == nil {
			r.Reply(textResult("no such tool: "+p.Name, true), nil)
			return
		}
		if len(p.Arguments) == 0 {
			p.Arguments = json.RawMessage("{}")
		}
		call := s.tools[i].Call
		s.wg.Go(func() {
			out, err := call(s.ctx, p.Arguments)
			if err != nil {
				r.Reply(textResult(err.Error(), true), nil)
				return
			}
			r.Reply(textResult(out, false), nil)
		})
	default:
		r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "method not found: " + r.Method})
	}
}

func textResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text}}, "isError": isErr}
}

// Close stops listening, ends the tool calls running and removes the socket. A nil Server closes as nothing.
func (s *Server) Close() error {
	if s == nil || s.ln == nil {
		return nil
	}
	err := s.ln.Close()
	s.stop()
	s.mu.Lock()
	for _, c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	os.RemoveAll(s.dir)
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	return err
}

// Stdio is the MCP stdio server an agent starts to reach a Server: `oriel hook mcp`, with the socket in its
// environment.
type Stdio struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Entry is how an agent starts the stdio side of the server on sock. Outside a sandbox that's self (this binary, ""
// for os.Executable) and the socket; in one it's the sandbox's own oriel and the socket's end there. Either way
// the agent's own process carries SockEnv=sock, which is how a sandbox knows to bring the socket in.
func Entry(sock, self string, sb *agent.Sandbox) Stdio {
	if sb != nil {
		return Stdio{Name: "oriel", Command: "oriel", Args: []string{"hook", "mcp"}, Env: map[string]string{SockEnv: sb.HookSocket}}
	}
	if self == "" {
		self = "oriel"
		if exe, err := os.Executable(); err == nil {
			self = exe
		}
	}
	return Stdio{Name: "oriel", Command: self, Args: []string{"hook", "mcp"}, Env: map[string]string{SockEnv: sock}}
}
