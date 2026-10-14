package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/jsonrpc"
	"github.com/liamg/oriel/core"
)

// TestMain makes the test binary the fake codex (`app-server`, `--version`) and `oriel hook mcp` when run so.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "app-server", "--version":
			os.Exit(fakeCodex(os.Args[1:]))
		case "hook":
			if len(os.Args) > 2 && os.Args[2] == "mcp" {
				os.Exit(MCPMain(os.Stdin, os.Stdout, os.Environ()))
			}
		}
	}
	os.Exit(m.Run())
}

func self(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func fakeProvider(t *testing.T) *Provider {
	exe := self(t)
	return &Provider{Bin: exe, OrielBin: exe}
}

func fakeSpec(t *testing.T, dir string) agent.StartSpec {
	return agent.StartSpec{
		Env:      []string{"CODEX_HOME=" + filepath.Join(dir, ".fake-codex")},
		StateDir: t.TempDir(),
		Policy:   agent.PolicyAsk,
	}
}

func TestConformance(t *testing.T) {
	agenttest.Run(t, agenttest.Harness{Provider: fakeProvider(t), Spec: fakeSpec, Timeout: 20 * time.Second})
}

func TestTurnEvents(t *testing.T) {
	h := agenttest.Harness{Provider: fakeProvider(t), Spec: fakeSpec, Timeout: 20 * time.Second}
	s := agenttest.Start(t, h)
	s.Send("say hello")
	s.Reply()
	var ready, partial, thinking, user bool
	var end agent.TurnEnd
	var usage *agent.UsageUpdate
	for _, ev := range s.Events {
		switch ev := ev.(type) {
		case agent.Ready:
			ready = ev.ID != ""
		case agent.Message:
			switch {
			case ev.Partial && ev.Role == core.ItemAssistant:
				partial = true
			case ev.Role == core.ItemThinking && ev.Text == "greeting":
				thinking = true
			case ev.Role == core.ItemUser && ev.Text == "say hello":
				user = true
			}
		case agent.TurnEnd:
			end = ev
		case agent.UsageUpdate:
			usage = &ev
		}
	}
	if !ready || !partial || !thinking || !user {
		t.Errorf("ready %v, partial %v, thinking %v, user %v", ready, partial, thinking, user)
	}
	if end.Usage.Input != 20 || end.Usage.Output != 10 || end.Usage.ContextMax != 272000 || end.Usage.Duration != 12*time.Millisecond {
		t.Errorf("usage = %+v", end.Usage)
	}
	if usage == nil || usage.Usage.Provider != "codex" || len(usage.Usage.Windows) != 1 || usage.Usage.Windows[0].Name != "5h" {
		t.Errorf("usage update = %+v", usage)
	}
}

// The pack's text comes before the first prompt, once.
func TestPackPreamble(t *testing.T) {
	h := agenttest.Harness{Provider: fakeProvider(t), Spec: func(t *testing.T, dir string) agent.StartSpec {
		s := fakeSpec(t, dir)
		s.Pack = agent.Pack{Name: "work", Text: "You are working on a ticket."}
		s.Prompt = "say hello"
		return s
	}, Timeout: 20 * time.Second}
	s := agenttest.Start(t, h)
	s.Reply()
	s.Send("what did I say")
	if got := s.Reply(); !strings.Contains(got, "You are working on a ticket.\n\nsay hello") {
		t.Errorf("reply = %q", got)
	}
}

func TestRefusesAPIKey(t *testing.T) {
	p := fakeProvider(t)
	for _, tc := range []struct {
		account string
		env     []string
		want    string
	}{
		{"apiKey", nil, "billed per token"},
		{"none", nil, "isn't signed in"},
		{"apiKey", []string{AllowAPIKey + "=1"}, ""},
	} {
		t.Run(tc.account+strings.Join(tc.env, ","), func(t *testing.T) {
			dir := t.TempDir()
			spec := fakeSpec(t, dir)
			spec.Cwd = dir
			spec.Env = append(spec.Env, "FAKE_CODEX_ACCOUNT="+tc.account)
			spec.Env = append(spec.Env, tc.env...)
			s, err := p.Start(context.Background(), spec)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				s.Close()
				return
			}
			if err == nil {
				s.Close()
				t.Fatal("started")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// AI-13: a session that can't start for want of a login says so as AuthExpired, with the command that signs in.
func TestStartSignedOutIsAuthExpired(t *testing.T) {
	p := fakeProvider(t)
	dir := t.TempDir()
	spec := fakeSpec(t, dir)
	spec.Cwd = dir
	spec.Env = append(spec.Env, "FAKE_CODEX_ACCOUNT=none")
	_, err := p.Start(context.Background(), spec)
	var ae agent.AuthExpired
	if !errors.As(err, &ae) || ae.Provider != "codex" || ae.Shell != "codex login" || ae.Terminal != "" {
		t.Errorf("err = %#v, want AuthExpired", err)
	}
}

func TestRequireChatGPT(t *testing.T) {
	read := func(s string) account {
		var a account
		if err := json.Unmarshal([]byte(s), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	if err := requireChatGPT(read(`{"account":{"type":"chatgpt","email":"a@b","planType":"plus"},"requiresOpenaiAuth":true}`), nil); err != nil {
		t.Error(err)
	}
	err := requireChatGPT(read(`{"account":{"type":"apiKey"},"requiresOpenaiAuth":true}`), nil)
	if err == nil || !strings.Contains(err.Error(), "billed per token") || !strings.Contains(err.Error(), AllowAPIKey+"=1") {
		t.Errorf("api key: %v", err)
	}
	if err := requireChatGPT(read(`{"account":{"type":"amazonBedrock"},"requiresOpenaiAuth":false}`), nil); err == nil {
		t.Error("bedrock ran without the opt-in")
	}
	if err := requireChatGPT(read(`{"account":null,"requiresOpenaiAuth":true}`), []string{AllowAPIKey + "=1"}); err == nil {
		t.Error("ran signed out")
	}
	if err := requireChatGPT(read(`{"account":{"type":"apiKey"}}`), []string{AllowAPIKey + "=1"}); err != nil {
		t.Error(err)
	}
}

func TestDetectModelsUsage(t *testing.T) {
	p := fakeProvider(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	in, err := p.Detect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if in.Version != "0.99.0-fake" || !in.LoggedIn || in.Account != "chatgpt" || in.Note != "" {
		t.Errorf("install = %+v", in)
	}
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 1 || models[0].ID != "gpt-fake-codex" || !models[0].Default || strings.Join(models[0].Efforts, ",") != "low,high" {
		t.Errorf("models = %+v, %v", models, err)
	}
	u, err := p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Plan != "ChatGPT Pro" || len(u.Windows) != 2 || u.Windows[0].Name != "5h" || u.Windows[0].Used != 0.12 ||
		u.Windows[1].Name != "weekly" || !u.Windows[1].Resets.Equal(time.Unix(1700500000, 0)) {
		t.Errorf("usage = %+v", u)
	}
}

func TestRateLimits(t *testing.T) {
	var r rateLimits
	json.Unmarshal([]byte(`{"limitId":"codex","primary":{"usedPercent":50,"windowDurationMins":300,"resetsAt":100},
		"secondary":{"usedPercent":5,"windowDurationMins":10080,"resetsAt":200},
		"credits":{"hasCredits":true,"unlimited":false,"balance":"12.50"},"planType":"pro_lite"}`), &r)
	now := time.Unix(50, 0)
	u := r.usage(now)
	want := core.ProviderUsage{Provider: "codex", Plan: "ChatGPT Pro Lite", Credits: "12.50", Sampled: now, Source: "Codex's account/rateLimits/read", Windows: []core.UsageWindow{
		{Name: "5h", Used: 0.5, Resets: time.Unix(100, 0), Length: 5 * time.Hour},
		{Name: "weekly", Used: 0.05, Resets: time.Unix(200, 0), Length: 7 * 24 * time.Hour},
	}}
	if b, w := jsonOf(u), jsonOf(want); string(b) != string(w) {
		t.Errorf("usage = %s\nwant    %s", b, w)
	}
	// A sparse update keeps what it leaves out.
	var up rateLimits
	json.Unmarshal([]byte(`{"primary":{"usedPercent":60,"windowDurationMins":300,"resetsAt":100}}`), &up)
	m := r.merge(up).usage(now)
	if m.Plan != "ChatGPT Pro Lite" || len(m.Windows) != 2 || m.Windows[0].Used != 0.6 || m.Credits != "12.50" {
		t.Errorf("merged = %+v", m)
	}
	// Without lengths, the first window is the 5-hour one.
	var bare rateLimits
	json.Unmarshal([]byte(`{"primary":{"usedPercent":1},"secondary":{"usedPercent":2},"credits":{"hasCredits":false,"unlimited":true}}`), &bare)
	if b := bare.usage(now); b.Windows[0].Name != "5h" || b.Windows[1].Name != "weekly" || b.Credits != "unlimited" || b.Plan != "" {
		t.Errorf("bare = %+v", b)
	}
	if n := windowName(new(int64(1440)), 0); n != "1d" {
		t.Errorf("a day is %q", n)
	}
}

func TestGate(t *testing.T) {
	for _, tc := range []struct {
		policy            agent.Policy
		sandboxed         bool
		approval, sandbox string
		fails             bool
	}{
		{agent.PolicyAsk, false, "untrusted", "workspace-write", false},
		{"", false, "untrusted", "workspace-write", false},
		{agent.PolicyLight, false, "on-request", "workspace-write", false},
		{agent.PolicyBypass, false, "", "", true},
		{agent.PolicyBypass, true, "never", "danger-full-access", false},
	} {
		s := agent.StartSpec{Policy: tc.policy}
		if tc.sandboxed {
			s.Sandbox = &agent.Sandbox{Name: "x"}
		}
		a, sb, err := gate(s)
		if a != tc.approval || sb != tc.sandbox || (err != nil) != tc.fails {
			t.Errorf("%q sandboxed %v: %q %q %v", tc.policy, tc.sandboxed, a, sb, err)
		}
	}
}

func TestParseDiff(t *testing.T) {
	d := parseDiff("a.go", "update", "--- a/a.go\n+++ b/a.go\n@@ -3,3 +3,3 @@\n keep\n-old\n+new\n keep\n")
	want := core.FileDiff{Path: "a.go", Added: 1, Removed: 1, Lines: []core.DiffLine{
		{Op: " ", Old: 3, New: 3, Text: "keep"}, {Op: "-", Old: 4, Text: "old"}, {Op: "+", New: 4, Text: "new"}, {Op: " ", Old: 5, New: 5, Text: "keep"},
	}}
	if b, w := jsonOf(d), jsonOf(want); string(b) != string(w) {
		t.Errorf("diff = %s\nwant   %s", b, w)
	}
	if a := parseDiff("n.go", "add", "one\ntwo\n"); a.Added != 2 || a.Lines[1].New != 2 || a.Lines[1].Op != "+" {
		t.Errorf("add = %+v", a)
	}
}

// A file change's approval names its files, taken from the item that started it.
func TestFileChangePermission(t *testing.T) {
	s := &session{events: make(chan agent.Event, 16), closing: make(chan struct{}), id: "th",
		partial: map[string]string{}, started: map[string]bool{}, changes: map[string][]fileChange{},
		pending: map[string]func(agent.Decision) error{}, forks: map[string]*forkTurn{}}
	s.onNotify("item/started", json.RawMessage(`{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"fc","status":"inProgress",
		"changes":[{"path":"a.go","kind":{"type":"update","move_path":null},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}}`))
	call := (<-s.events).(agent.ToolCall)
	if call.Tool != "Edit" || call.ID != "fc" || !strings.Contains(string(call.Input), "a.go") {
		t.Errorf("call = %+v", call)
	}
	ar, aw := io.Pipe()
	br, bw := io.Pipe()
	s.rpc = jsonrpc.NewConn(ar, bw, jsonrpc.Handlers{Request: s.onRequest}, jsonrpc.Options{})
	codex := jsonrpc.NewConn(br, aw, jsonrpc.Handlers{}, jsonrpc.Options{})
	defer s.rpc.Close()
	defer codex.Close()
	answer := make(chan string, 1)
	go func() {
		var r struct{ Decision string }
		codex.Call(context.Background(), "item/fileChange/requestApproval", map[string]any{"threadId": "th", "turnId": "tu", "itemId": "fc", "startedAtMs": 1}, &r)
		answer <- r.Decision
	}()
	p := (<-s.events).(agent.Permission)
	if p.Tool != "Edit" || p.Summary != "a.go" || !strings.Contains(p.Rule, "a.go") {
		t.Errorf("permission = %+v", p)
	}
	if err := s.Answer(context.Background(), p.ID, agent.Decision{Allow: true, Always: true}); err != nil {
		t.Fatal(err)
	}
	if d := <-answer; d != "acceptForSession" {
		t.Errorf("decision = %q", d)
	}
	if err := s.Answer(context.Background(), p.ID, agent.Decision{Allow: true}); err == nil {
		t.Error("answered twice")
	}
	s.onNotify("item/completed", json.RawMessage(`{"threadId":"th","turnId":"tu","item":{"type":"fileChange","id":"fc","status":"completed",
		"changes":[{"path":"a.go","kind":{"type":"update"},"diff":"@@ -1 +1 @@\n-a\n+b\n"}]}}`))
	res := (<-s.events).(agent.ToolResult)
	if res.Diff == nil || res.Diff.Path != "a.go" || res.Diff.Added != 1 || res.Diff.Removed != 1 || res.IsError {
		t.Errorf("result = %+v", res)
	}
}

// The MCP server answers initialize itself and passes tools to the session's socket.
func TestMCPServer(t *testing.T) {
	tools, err := serveTools([]agent.Tool{agenttest.Echo, {Name: "fail", ReadOnly: true, Call: func(context.Context, json.RawMessage) (string, error) {
		return "", io.ErrUnexpectedEOF
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer tools.Close()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	code := make(chan int, 1)
	go func() { code <- MCPMain(inR, outW, []string{SockEnv + "=" + tools.path}); outW.Close() }()
	out := bufio.NewReader(outR)
	send := func(s string) map[string]any {
		t.Helper()
		io.WriteString(inW, s+"\n")
		line, err := out.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["jsonrpc"] != "2.0" {
			t.Errorf("no jsonrpc 2.0 in %s", line)
		}
		return m
	}
	init := send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	if r := init["result"].(map[string]any); r["protocolVersion"] != "2025-03-26" || r["serverInfo"].(map[string]any)["name"] != "oriel" {
		t.Errorf("initialize = %v", init)
	}
	io.WriteString(inW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	list := send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	b, _ := json.Marshal(list["result"])
	if !strings.Contains(string(b), `"name":"echo"`) || !strings.Contains(string(b), `"readOnlyHint":true`) || !strings.Contains(string(b), `"inputSchema":{"type":"object"}`) {
		t.Errorf("tools = %s", b)
	}
	call := send(`{"jsonrpc":"2.0","id":"c","method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}`)
	b, _ = json.Marshal(call)
	if !strings.Contains(string(b), `"text":"echo: hi"`) || !strings.Contains(string(b), `"isError":false`) || call["id"] != "c" {
		t.Errorf("call = %s", b)
	}
	fail := send(`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fail"}}`)
	if b, _ = json.Marshal(fail); !strings.Contains(string(b), `"isError":true`) {
		t.Errorf("fail = %s", b)
	}
	missing := send(`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"nope","arguments":{}}}`)
	if b, _ = json.Marshal(missing); !strings.Contains(string(b), "no such tool") {
		t.Errorf("missing = %s", b)
	}
	unknown := send(`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`)
	if e, _ := unknown["error"].(map[string]any); e == nil || e["code"] != float64(jsonrpc.MethodNotFound) {
		t.Errorf("unknown = %v", unknown)
	}
	inW.Close()
	select {
	case c := <-code:
		if c != 0 {
			t.Errorf("exit = %d", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("MCPMain didn't return at EOF")
	}
}

func TestMCPMainWithoutSocket(t *testing.T) {
	if c := MCPMain(strings.NewReader(""), io.Discard, nil); c == 0 {
		t.Error("ran without a socket")
	}
	path := filepath.Join(t.TempDir(), "gone.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	if c := MCPMain(strings.NewReader(""), io.Discard, []string{SockEnv + "=" + path}); c == 0 {
		t.Error("ran without the session")
	}
}

func TestGatewayConfig(t *testing.T) {
	f := filepath.Join(t.TempDir(), "gw.json")
	os.WriteFile(f, []byte(`{"mcpServers":{"gw":{"type":"http","url":"http://gw/mcp","headers":{"X":"y"}},"local":{"command":"x","args":["a"]}}}`), 0o644)
	s := &session{p: &Provider{OrielBin: "/bin/oriel"}, tools: &toolServer{path: "/tmp/s.sock"},
		spec: agent.StartSpec{Sandbox: &agent.Sandbox{GatewayMCP: f}}}
	cfg, err := s.config()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	for _, want := range []string{
		`"mcp_servers.oriel":{"args":["hook","mcp"],"command":"/bin/oriel","default_tools_approval_mode":"approve","env":{"ORIEL_HOOK_SOCK":"/tmp/s.sock"}}`,
		`"mcp_servers.gw":{"http_headers":{"X":"y"},"url":"http://gw/mcp"}`,
		`"mcp_servers.local":{"args":["a"],"command":"x"}`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("config = %s\nwant %s", b, want)
		}
	}
}
