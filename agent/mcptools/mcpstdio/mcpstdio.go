// Package mcpstdio is `oriel hook mcp`: an MCP stdio server named oriel whose tools are a session's, answered over
// the unix socket in ORIEL_HOOK_SOCK by whoever serves them (agent/mcptools). It speaks MCP with sandbox/mcp's
// server, which the sandbox's static helper links anyway.
package mcpstdio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/sandbox/mcp"
)

// SockEnv names the unix socket Main dials: agent.HookSocketEnv.
const SockEnv = "ORIEL_HOOK_SOCK"

// Main serves MCP over stdin and stdout, passing tools/list and tools/call to the socket named in env. It returns
// the exit code; errors go to stderr.
func Main(stdin io.Reader, stdout, stderr io.Writer, env []string) int {
	sock := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, SockEnv+"="); ok {
			sock = v
		}
	}
	if sock == "" {
		_, _ = fmt.Fprintln(stderr, "oriel hook mcp: "+SockEnv+" isn't set")
		return 2
	}
	nc, err := net.Dial("unix", sock)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "oriel hook mcp:", err)
		return 1
	}
	srv := &mcp.Server{Name: "oriel", Version: "0"}
	host := jsonrpc.NewConn(nc, nc, jsonrpc.Handlers{Notify: func(method string, _ json.RawMessage) {
		if method == "notifications/tools/list_changed" {
			srv.NotifyToolsChanged()
		}
	}}, jsonrpc.Options{Version: "2.0"})
	defer func() { _ = host.Close() }()
	srv.Handler = forward{host}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gone := make(chan struct{})
	go func() {
		select {
		case <-host.Done():
			close(gone)
			cancel()
		case <-ctx.Done():
		}
	}()
	_ = srv.Serve(ctx, stdin, stdout)
	select {
	case <-gone:
		_, _ = fmt.Fprintln(stderr, "oriel hook mcp: the session is gone")
		return 1
	default:
		return 0
	}
}

// forward answers the server's tools from the session's socket.
type forward struct{ host *jsonrpc.Conn }

func (f forward) Tools(ctx context.Context) ([]mcp.Tool, error) {
	var res struct {
		Tools []mcp.Tool `json:"tools"`
	}
	err := f.host.Call(ctx, "tools/list", nil, &res)
	return res.Tools, err
}

func (f forward) Call(ctx context.Context, name string, args json.RawMessage) (mcp.CallResult, error) {
	var res mcp.CallResult
	err := f.host.Call(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, &res)
	return res, err
}
