package rpcagent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/jsonrpc"
)

// rawPlugin is a plugin written against the protocol alone, as one in another language would be: it asks for
// Oriel's tools as an MCP server, and runs a process through Oriel.
type rawPlugin struct {
	conn  *jsonrpc.Conn
	specs chan startParams
	out   chan string
	mu    sync.Mutex
	text  string
}

func (p *rawPlugin) handle(r *jsonrpc.Request) {
	switch r.Method {
	case "initialize":
		r.Reply(map[string]any{"protocolVersion": 1, "id": "raw", "features": map[string]any{"images": true}, "mcp": true}, nil)
	case "session/start":
		var sp startParams
		json.Unmarshal(r.Params, &sp)
		go func() {
			err := p.conn.Call(context.Background(), "exec/start", map[string]any{"session": sp.Session, "process": "p1",
				"args": []string{"sh", "-c", "echo sock=$" + mcpstdio.SockEnv}}, nil)
			if err != nil {
				p.out <- "exec/start: " + err.Error()
			}
			p.specs <- sp
			r.Reply(map[string]any{"id": "raw-1"}, nil)
		}()
	case "session/close":
		r.Reply(map[string]any{}, nil)
	default:
		r.Reply(nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: r.Method})
	}
}

func (p *rawPlugin) notify(method string, params json.RawMessage) {
	var o output
	json.Unmarshal(params, &o)
	switch method {
	case "exec/output":
		p.mu.Lock()
		p.text += string(unb64(o.Data))
		p.mu.Unlock()
	case "exec/exit":
		p.mu.Lock()
		p.out <- p.text
		p.mu.Unlock()
	}
}

// ref:   acp/docs/session-setup.mdx#Stdio Transport
// quote: "All Agents **MUST** support connecting to MCP servers via stdio"
// A plugin that asks for them gets the session's tools as an MCP stdio server to hand its agent, `oriel hook mcp`
// with the socket named; the processes it runs through Oriel carry the socket, and its session/tool calls the
// tools straight.
func TestPluginAsksForTheToolsAsAnMCPServer(t *testing.T) {
	isolate.Test(t)
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	p := &rawPlugin{specs: make(chan startParams, 1), out: make(chan string, 2)}
	p.conn = jsonrpc.NewConn(br, bw, jsonrpc.Handlers{Request: p.handle, Notify: p.notify}, jsonrpc.Options{Version: "2.0"})
	defer p.conn.Close()
	c, err := Dial(context.Background(), Options{Name: "raw", Dial: Pipe(ar, aw), Self: "/opt/oriel"})
	if err != nil {
		t.Fatal(err)
	}
	defer closer(c).Close()
	if !agent.FeaturesOf(c).Images {
		t.Errorf("features = %+v", agent.FeaturesOf(c))
	}
	s, err := c.Start(context.Background(), agent.StartSpec{Exec: hostexec.Exec{}, Tools: []agent.Tool{agenttest.Echo}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sp := <-p.specs
	m := sp.Spec.MCP
	if m == nil || m.Command != "/opt/oriel" || strings.Join(m.Args, " ") != "hook mcp" || m.Env[mcpstdio.SockEnv] != m.Socket || m.Socket == "" {
		t.Fatalf("the MCP server = %+v", m)
	}
	select {
	case got := <-p.out:
		if strings.TrimSpace(got) != "sock="+m.Socket {
			t.Errorf("the process printed %q, want the socket", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the process never exited")
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	code := make(chan int, 1)
	go func() {
		code <- mcpstdio.Main(inR, outW, io.Discard, []string{mcpstdio.SockEnv + "=" + m.Socket})
		outW.Close()
	}()
	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`+"\n")
	line, _ := bufio.NewReader(outR).ReadString('\n')
	if !strings.Contains(line, "echo: hi") {
		t.Errorf("tools/call = %s", line)
	}
	inW.Close()
	<-code

	var r toolResult
	if err := p.conn.Call(context.Background(), "session/tool", map[string]any{"session": sp.Session, "name": "echo",
		"arguments": map[string]string{"text": "yo"}}, &r); err != nil || r.Output != "echo: yo" {
		t.Errorf("session/tool = %+v %v", r, err)
	}
}
