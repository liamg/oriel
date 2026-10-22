// Package mcpstdio is `oriel hook mcp`: an MCP stdio server named oriel whose tools are a session's, answered over
// the unix socket in ORIEL_HOOK_SOCK by whoever serves them (agent/mcptools). It needs nothing but jsonrpc, so the
// sandbox's static helper links it too.
package mcpstdio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/jsonrpc"
)

// SockEnv names the unix socket Main dials: agent.HookSocketEnv.
const SockEnv = "ORIEL_HOOK_SOCK"

// Version is the MCP revision offered when the client asks for one this server doesn't know.
const Version = "2025-06-18"

// Versions are the MCP revisions it speaks, newest first.
var Versions = []string{Version, "2025-03-26", "2024-11-05"}

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
		fmt.Fprintln(stderr, "oriel hook mcp: "+SockEnv+" isn't set")
		return 2
	}
	nc, err := net.Dial("unix", sock)
	if err != nil {
		fmt.Fprintln(stderr, "oriel hook mcp:", err)
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
			v := Version
			if slices.Contains(Versions, p.ProtocolVersion) {
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
		fmt.Fprintln(stderr, "oriel hook mcp: the session is gone")
		cancel()
		wg.Wait()
		return 1
	}
}
