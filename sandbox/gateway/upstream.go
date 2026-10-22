package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox/mcp"
)

// Server is one upstream MCP server, as the user's Claude config describes it.
type Server struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"` // stdio | http
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Source says where it was found: "user", "plugin:<name>" or "oriel".
	Source string `json:"source"`
}

type rawServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// Discover lists the MCP servers the user installed, as the providers found them (agent.Sandboxed's MCPServers),
// then oriel's own list. The first of a name wins.
func Discover(installed []agent.MCPServer, extra map[string]json.RawMessage, hidden []string) []Server {
	var out []Server
	seen := map[string]bool{}
	add := func(name, source string, raw rawServer, vars map[string]string) {
		name = cleanName(name)
		if name == "" || name == "oriel" || seen[name] || slices.Contains(hidden, name) {
			return
		}
		s, ok := toServer(name, source, raw, vars)
		if !ok {
			return
		}
		seen[name] = true
		out = append(out, s)
	}
	for _, m := range installed {
		var raw rawServer
		if json.Unmarshal(m.Config, &raw) == nil {
			add(m.Name, m.Source, raw, m.Vars)
		}
	}
	names := make([]string, 0, len(extra))
	for k := range extra {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		var raw rawServer
		if json.Unmarshal(extra[name], &raw) == nil {
			add(name, "oriel", raw, nil)
		}
	}
	return out
}

func toServer(name, source string, raw rawServer, vars map[string]string) (Server, bool) {
	s := Server{Name: name, Source: source, Env: map[string]string{}, Headers: map[string]string{}}
	switch raw.Type {
	case "", "stdio":
		if raw.Command == "" {
			return Server{}, false
		}
		s.Type = "stdio"
		s.Command = expand(raw.Command, vars)
		for _, a := range raw.Args {
			s.Args = append(s.Args, expand(a, vars))
		}
		for k, v := range raw.Env {
			s.Env[k] = expand(v, vars)
		}
	case "http", "streamable-http":
		if raw.URL == "" {
			return Server{}, false
		}
		s.Type = "http"
		s.URL = expand(raw.URL, vars)
		for k, v := range raw.Headers {
			s.Headers[k] = expand(v, vars)
		}
	default:
		// The legacy SSE transport is not proxied.
		return Server{}, false
	}
	return s, true
}

var varRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expand resolves ${VAR} and ${VAR:-default} the way Claude Code does.
func expand(s string, vars map[string]string) string {
	return varRef.ReplaceAllStringFunc(s, func(m string) string {
		p := varRef.FindStringSubmatch(m)
		if v, ok := vars[p[1]]; ok {
			return v
		}
		if v, ok := os.LookupEnv(p[1]); ok {
			return v
		}
		return p[3]
	})
}

var badName = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

func cleanName(s string) string {
	return strings.Trim(badName.ReplaceAllString(strings.ToLower(s), "_"), "_")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ToolName is how an upstream tool is offered: <server>__<tool>, within the
// 64 characters a tool name may have.
func ToolName(server, tool string) string {
	name := server + "__" + badName.ReplaceAllString(tool, "_")
	if len(name) <= 64 {
		return name
	}
	sum := sha256.Sum256([]byte(name))
	return name[:55] + "_" + hex.EncodeToString(sum[:])[:8]
}

// Upstreams holds the connections to the user's servers.
type Upstreams struct {
	Servers []Server
	// Auth supplies OAuth for an http server; nil means none.
	Auth func(s Server) mcp.TokenSource
	// OnChange is told when a server's tool list changes.
	OnChange func()

	mu    sync.Mutex
	conns map[string]*conn
}

type conn struct {
	mu     sync.Mutex
	client *mcp.Client
	tools  []mcp.Tool
	err    error
	at     time.Time
}

// Tool is an upstream tool as the gateway offers it.
type Tool struct {
	mcp.Tool
	Server   string
	Upstream string // the tool's own name on its server
}

// ServerError is a server that could not be reached, for the UI.
type ServerError struct {
	Server string `json:"server"`
	Error  string `json:"error"`
}

// Tools lists every reachable server's tools, connecting as needed.
func (u *Upstreams) Tools(ctx context.Context) ([]Tool, []ServerError) {
	var out []Tool
	var errs []ServerError
	for _, s := range u.Servers {
		c := u.conn(s.Name)
		tools, err := c.list(ctx, u, s)
		if err != nil {
			errs = append(errs, ServerError{Server: s.Name, Error: err.Error()})
			continue
		}
		for _, t := range tools {
			name := ToolName(s.Name, t.Name)
			ot := t
			ot.Name = name
			out = append(out, Tool{Tool: ot, Server: s.Name, Upstream: t.Name})
		}
	}
	return out, errs
}

// Call runs an upstream tool.
func (u *Upstreams) Call(ctx context.Context, t Tool, args json.RawMessage) (mcp.CallResult, error) {
	for _, s := range u.Servers {
		if s.Name != t.Server {
			continue
		}
		c := u.conn(s.Name)
		client, err := c.dial(ctx, u, s)
		if err != nil {
			return mcp.CallResult{}, err
		}
		return client.CallTool(ctx, t.Upstream, args)
	}
	return mcp.CallResult{}, fmt.Errorf("no server %q", t.Server)
}

// Close disconnects every server.
func (u *Upstreams) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, c := range u.conns {
		c.mu.Lock()
		if c.client != nil {
			_ = c.client.Close()
		}
		c.mu.Unlock()
	}
	u.conns = nil
}

func (u *Upstreams) conn(name string) *conn {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.conns == nil {
		u.conns = map[string]*conn{}
	}
	c := u.conns[name]
	if c == nil {
		c = &conn{}
		u.conns[name] = c
	}
	return c
}

// retryAfter keeps a server that failed from being dialled on every list.
const retryAfter = time.Minute

func (c *conn) list(ctx context.Context, u *Upstreams, s Server) ([]mcp.Tool, error) {
	c.mu.Lock()
	if c.tools != nil {
		defer c.mu.Unlock()
		return c.tools, nil
	}
	if c.err != nil && time.Since(c.at) < retryAfter {
		defer c.mu.Unlock()
		return nil, c.err
	}
	c.mu.Unlock()
	client, err := c.dial(ctx, u, s)
	if err != nil {
		return nil, err
	}
	tools, err := client.ListTools(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tools, c.err, c.at = tools, err, time.Now()
	return tools, err
}

func (c *conn) dial(ctx context.Context, u *Upstreams, s Server) (*mcp.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.client != nil {
		return c.client, nil
	}
	var client *mcp.Client
	var err error
	switch s.Type {
	case "stdio":
		cmd := exec.Command(s.Command, s.Args...)
		cmd.Env = os.Environ()
		for k, v := range s.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		client, err = mcp.DialStdio(dctx, cmd)
		cancel()
	case "http":
		hdr := http.Header{}
		for k, v := range s.Headers {
			hdr.Set(k, v)
		}
		var auth mcp.TokenSource
		if u.Auth != nil {
			auth = u.Auth(s)
		}
		// The dial itself may wait on the user signing in.
		client, err = mcp.DialHTTP(ctx, s.URL, hdr, auth)
	default:
		err = errors.New("unsupported transport")
	}
	if err != nil {
		c.err, c.at = err, time.Now()
		return nil, err
	}
	client.OnToolsChanged(func() {
		c.mu.Lock()
		c.tools = nil
		c.mu.Unlock()
		if u.OnChange != nil {
			u.OnChange()
		}
	})
	c.client, c.err = client, nil
	return client, nil
}
