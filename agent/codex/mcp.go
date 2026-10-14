package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/jsonrpc"
)

// SockEnv names the unix socket `oriel hook mcp` dials to reach the session's tools.
const SockEnv = agent.HookSocketEnv

// mcpVersion is the MCP revision offered when the client asks for one this server doesn't know.
const mcpVersion = "2025-06-18"

var mcpVersions = []string{mcpVersion, "2025-03-26", "2024-11-05"}

// MCPMain is `oriel hook mcp`: an MCP stdio server named oriel whose tools are the session's, answered by the
// session host over the socket in ORIEL_HOOK_SOCK. It returns the exit code.
func MCPMain(stdin io.Reader, stdout io.Writer, env []string) int {
	sock := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, SockEnv+"="); ok {
			sock = v
		}
	}
	if sock == "" {
		fmt.Fprintln(os.Stderr, "oriel hook mcp: "+SockEnv+" isn't set")
		return 2
	}
	nc, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel hook mcp:", err)
		return 1
	}
	host := jsonrpc.NewConn(nc, nc, jsonrpc.Handlers{}, jsonrpc.Options{Version: "2.0"})
	defer host.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	forward := func(r *jsonrpc.Request) {
		wg.Go(func() {
			res, err := host.CallRaw(ctx, r.Method, r.Params)
			r.Reply(res, err)
		})
	}
	client := jsonrpc.NewConn(stdin, stdout, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		switch r.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			json.Unmarshal(r.Params, &p)
			v := mcpVersion
			if slices.Contains(mcpVersions, p.ProtocolVersion) {
				v = p.ProtocolVersion
			}
			r.Reply(map[string]any{
				"protocolVersion": v,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "oriel", "version": "0"},
			}, nil)
		case "ping":
			r.Reply(map[string]any{}, nil)
		case "tools/list", "tools/call":
			forward(r)
		default:
			r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "method not found: " + r.Method})
		}
	}}, jsonrpc.Options{Version: "2.0"})
	select {
	case <-client.Done():
		wg.Wait()
		return 0
	case <-host.Done():
		fmt.Fprintln(os.Stderr, "oriel hook mcp: the session is gone")
		cancel()
		wg.Wait()
		return 1
	}
}

// toolServer answers tools/list and tools/call for `oriel hook mcp` on a unix socket.
type toolServer struct {
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

// serveTools listens in a fresh short directory: a unix socket's path has to fit in about 100 bytes.
func serveTools(tools []agent.Tool) (*toolServer, error) {
	dir, err := os.MkdirTemp("", "oriel-codex-")
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
	s := &toolServer{dir: dir, path: path, ln: ln, tools: tools, ctx: ctx, stop: stop}
	s.wg.Go(s.accept)
	return s, nil
}

func (s *toolServer) accept() {
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

func (s *toolServer) handle(r *jsonrpc.Request) {
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

func (s *toolServer) Close() error {
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
