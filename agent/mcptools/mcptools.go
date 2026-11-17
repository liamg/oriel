// Package mcptools serves a session's Oriel tools to its agent as the MCP server oriel: a unix socket speaking MCP
// (sandbox/mcp's server, one per connection), which `oriel hook mcp` (mcpstdio) bridges to the agent over stdio.
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
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox/mcp"
)

// SockEnv names the socket `oriel hook mcp` (mcpstdio) dials.
const SockEnv = agent.HookSocketEnv

// Server answers tools/list and tools/call for `oriel hook mcp` on a unix socket.
type Server struct {
	dir   string
	path  string
	ln    net.Listener
	tools func() []agent.Tool
	ctx   context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup

	mu      sync.Mutex
	servers []*mcp.Server // one per connection
	names   []string      // the tools' names when last listed or checked
}

// recheck is how often the tools are compared with those listed: a change sends notifications/tools/list_changed.
const recheck = 10 * time.Second

// sockPathMax is how long a unix socket's path may be: sun_path is 104 bytes on macOS, with its NUL.
const sockPathMax = 103

// sockTail is the longest the path gets past the base directory: MkdirTemp's random suffix is up to 10 digits.
const sockTail = "/oriel-mcp-0123456789/tools.sock"

// Serve listens in a fresh short directory: the temp directory's, or /tmp's when that's too long. tools are those
// offered now, asked again at each list and call.
func Serve(tools func() []agent.Tool) (*Server, error) {
	base := os.TempDir()
	if len(base)+len(sockTail) > sockPathMax {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "oriel-mcp-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "tools.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	ctx, stop := context.WithCancel(context.Background())
	s := &Server{dir: dir, path: path, ln: ln, tools: tools, ctx: ctx, stop: stop, names: agent.ToolNames(tools())}
	s.wg.Go(s.accept)
	s.wg.Go(s.watch)
	return s, nil
}

// watch tells the clients when the tools change.
func (s *Server) watch() {
	t := time.NewTicker(recheck)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.changed(s.tools())
		}
	}
}

// changed notes the tools' names, and tells the clients when they differ from the last.
func (s *Server) changed(tools []agent.Tool) {
	names := agent.ToolNames(tools)
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(names, s.names) {
		return
	}
	s.names = names
	for _, srv := range s.servers {
		srv.NotifyToolsChanged()
	}
}

// Path is the socket's path, for SockEnv.
func (s *Server) Path() string { return s.path }

func (s *Server) accept() {
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		srv := &mcp.Server{Name: "oriel", Version: "0", Handler: tools{s}}
		s.mu.Lock()
		s.servers = append(s.servers, srv)
		s.mu.Unlock()
		s.wg.Go(func() {
			defer func() { _ = nc.Close() }()
			_ = srv.Serve(s.ctx, nc, nc)
			s.mu.Lock()
			s.servers = slices.DeleteFunc(s.servers, func(o *mcp.Server) bool { return o == srv })
			s.mu.Unlock()
		})
	}
}

// tools is the server's tools as an MCP handler.
type tools struct{ s *Server }

func (h tools) Tools(context.Context) ([]mcp.Tool, error) {
	offered := h.s.tools()
	h.s.changed(offered)
	out := make([]mcp.Tool, 0, len(offered))
	for _, t := range offered {
		m := mcp.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
		if t.ReadOnly {
			yes := true
			m.Annotations = &mcp.ToolAnnotations{ReadOnlyHint: &yes}
		}
		out = append(out, m)
	}
	return out, nil
}

func (h tools) Call(ctx context.Context, name string, args json.RawMessage) (mcp.CallResult, error) {
	offered := h.s.tools()
	i := slices.IndexFunc(offered, func(t agent.Tool) bool { return t.Name == name })
	if i < 0 || offered[i].Call == nil {
		return mcp.TextResult("no such tool: "+name, true), nil
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	out, err := offered[i].Call(ctx, args)
	if err != nil {
		return mcp.TextResult(err.Error(), true), nil
	}
	return mcp.TextResult(out, false), nil
}

// Close stops listening, ends the tool calls running and removes the socket. A nil Server closes as nothing.
func (s *Server) Close() error {
	if s == nil || s.ln == nil {
		return nil
	}
	err := s.ln.Close()
	s.stop()
	s.wg.Wait()
	_ = os.RemoveAll(s.dir)
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
