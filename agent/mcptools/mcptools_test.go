package mcptools

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
	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
	"github.com/liamg/oriel/internal/isolate"
)

// ref:   acp/docs/session-setup.mdx#Stdio Transport
// quote: "All Agents **MUST** support connecting to MCP servers via stdio (standard input/output)."
// `oriel hook mcp` answers initialize itself and passes the tools to the session's socket.
func TestServeOverStdio(t *testing.T) {
	isolate.Test(t)
	s, err := Serve(func() []agent.Tool {
		return []agent.Tool{agenttest.Echo, {Name: "fail", ReadOnly: true, Call: func(context.Context, json.RawMessage) (string, error) {
			return "", io.ErrUnexpectedEOF
		}}}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	code := make(chan int, 1)
	go func() { code <- mcpstdio.Main(inR, outW, io.Discard, []string{SockEnv + "=" + s.Path()}); outW.Close() }()
	out := bufio.NewReader(outR)
	send := func(msg string) string {
		t.Helper()
		io.WriteString(inW, msg+"\n")
		line, err := out.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return line
	}
	if got := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`); !strings.Contains(got, `"protocolVersion":"2025-03-26"`) || !strings.Contains(got, `"name":"oriel"`) {
		t.Errorf("initialize = %s", got)
	}
	if got := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`); !strings.Contains(got, `"name":"echo"`) || !strings.Contains(got, `"readOnlyHint":true`) {
		t.Errorf("tools/list = %s", got)
	}
	if got := send(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`); !strings.Contains(got, `"text":"echo: hi"`) || !strings.Contains(got, `"isError":false`) {
		t.Errorf("tools/call = %s", got)
	}
	if got := send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail"}}`); !strings.Contains(got, `"isError":true`) {
		t.Errorf("a failing tool = %s", got)
	}
	inW.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit = %d", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Main didn't return at EOF")
	}
}

// A tool offered after the server started is listed, and the client is told the list changed.
func TestToolsChangeAfterTheStart(t *testing.T) {
	isolate.Test(t)
	var mu sync.Mutex
	tools := []agent.Tool{agenttest.Echo}
	s, err := Serve(func() []agent.Tool {
		mu.Lock()
		defer mu.Unlock()
		return tools
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	code := make(chan int, 1)
	go func() { code <- mcpstdio.Main(inR, outW, io.Discard, []string{SockEnv + "=" + s.Path()}); outW.Close() }()
	out := bufio.NewReader(outR)
	read := func() string {
		t.Helper()
		line, err := out.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return line
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`+"\n")
	if got := read(); !strings.Contains(got, `"listChanged":true`) {
		t.Errorf("initialize = %s", got)
	}

	late := agenttest.Echo
	late.Name = "linear_issue"
	mu.Lock()
	tools = append(tools, late)
	mu.Unlock()
	io.WriteString(inW, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`+"\n")
	got := read() + read()
	if !strings.Contains(got, `"name":"linear_issue"`) || !strings.Contains(got, `"method":"notifications/tools/list_changed"`) {
		t.Errorf("after a tool was added = %s", got)
	}
	inW.Close()
	select {
	case <-code:
	case <-time.After(10 * time.Second):
		t.Fatal("Main didn't return at EOF")
	}
}

// Outside a sandbox the agent starts this binary on the host socket; inside one, the sandbox's oriel on the
// socket's end there.
func TestEntry(t *testing.T) {
	host := Entry("/tmp/s.sock", "/opt/oriel", nil)
	if host.Command != "/opt/oriel" || host.Env[SockEnv] != "/tmp/s.sock" || strings.Join(host.Args, " ") != "hook mcp" {
		t.Errorf("on the host: %+v", host)
	}
	in := Entry("/tmp/s.sock", "/opt/oriel", &agent.Sandbox{Name: "box", HookSocket: "/run/oriel/hook.sock"})
	if in.Command != "oriel" || in.Env[SockEnv] != "/run/oriel/hook.sock" {
		t.Errorf("in a sandbox: %+v", in)
	}
}
