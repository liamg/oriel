package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
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
	// Codex's input tokens include the cached ones: the turn's 20 are 15 fresh and 5 read from the cache.
	// ref:   codex/codex-rs/protocol/src/protocol.rs
	// quote: "pub fn non_cached_input(&self) -> i64 { (self.input_tokens - self.cached_input()).max(0) }"
	if end.Usage.Input != 15 || end.Usage.CacheRead != 5 || end.Usage.Output != 10 || end.Usage.ContextMax != 272000 ||
		end.Usage.Duration != 12*time.Millisecond {
		t.Errorf("usage = %+v", end.Usage)
	}
	if usage == nil || usage.Usage.Provider != "codex" || len(usage.Usage.Windows) != 1 || usage.Usage.Windows[0].Name != "Current session" {
		t.Errorf("usage update = %+v", usage)
	}
	// The turn is priced at the model thread/start said the thread runs on, which the fake's is gpt-5.5.
	// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs
	// quote: "pub struct ThreadStartResponse { pub thread: Thread, pub model: String,"
	want := price.Tokens{Input: 15, CacheRead: 5, Output: 10}.Cost(price.Default().RateFor("gpt-5.5", false, 0))
	if want == 0 || end.Usage.Cost != want {
		t.Errorf("cost = %v, want %v", end.Usage.Cost, want)
	}
}

// A turn on GPT-6 is priced at its listed rates, from the book the session was given, and the snapshot's without one.
//
// ref:   cost-dev/models.json
// quote: pb_openai_gpt-6.1-sol_direct_realtime_global_usd_20260929","verified_at":"2026-09-29T00:00:00Z","source_id":"source:openai-pricing","has_usage_based_tiers":true},"prices":{"cache_write_5m_tokens_per_1000000":"2.5","cached_input_tokens_per_1000000":"0.1","input_tokens_per_1000000":"2","output_tokens_per_1000000":"10"}
func TestGPT6TurnIsPriced(t *testing.T) {
	cheap, err := price.Parse([]byte(`{"items":[{"id":"openai:gpt-6.1-sol","prices":{"input_tokens_per_1000000":"1000000",` +
		`"output_tokens_per_1000000":"0","cached_input_tokens_per_1000000":"0"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		prices price.Books
		want   float64
	}{
		{nil, (15*2 + 5*0.1 + 10*10) / 1e6},
		{cheap, 15},
	} {
		h := agenttest.Harness{Provider: fakeProvider(t), Timeout: 20 * time.Second, Spec: func(t *testing.T, dir string) agent.StartSpec {
			sp := fakeSpec(t, dir)
			sp.Model, sp.Prices = "gpt-6.1-sol", c.prices
			return sp
		}}
		s := agenttest.Start(t, h)
		s.Send("say hello")
		s.Reply()
		var end agent.TurnEnd
		for _, ev := range s.Events {
			if e, ok := ev.(agent.TurnEnd); ok {
				end = e
			}
		}
		if math.Abs(end.Usage.Cost-c.want) > 1e-12 {
			t.Errorf("prices %v: cost = %v, want %v", c.prices != nil, end.Usage.Cost, c.want)
		}
	}
}

// The pack's body, without its frontmatter, is the thread's developer instructions: said once, as the thread starts,
// and kept by codex when it resumes, so never sent again nor shown as the user's.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs
// quote: "pub base_instructions: Option<String>, #[ts(optional = nullable)] pub developer_instructions: Option<String>,"
func TestPackInstructions(t *testing.T) {
	h := agenttest.Harness{Provider: fakeProvider(t), Spec: func(t *testing.T, dir string) agent.StartSpec {
		s := fakeSpec(t, dir)
		s.Pack = agent.Pack{Name: "work", Text: "---\nname: work\ndescription: Work on a ticket.\n---\nYou are working on a ticket.\n"}
		s.Prompt = "say hello"
		return s
	}, Timeout: 20 * time.Second}
	dir := t.TempDir()
	s := agenttest.StartIn(t, h, dir, "")
	s.Reply()
	s.Send("what are your instructions")
	if got := s.Reply(); got != "instructions: You are working on a ticket." {
		t.Errorf("reply = %q", got)
	}
	id := s.S.ID()
	s.S.Close()
	resumed := h
	resumed.Spec = func(t *testing.T, dir string) agent.StartSpec { sp := h.Spec(t, dir); sp.Prompt = ""; return sp }
	r := agenttest.StartIn(t, resumed, dir, id)
	r.Send("what are your instructions")
	if got := r.Reply(); got != "instructions: You are working on a ticket." {
		t.Errorf("resumed, the reply is %q: the instructions were sent again", got)
	}
	for _, ev := range s.Events {
		if m, ok := ev.(agent.Message); ok && m.Role == core.ItemUser && m.Text != "say hello" && m.Text != "what are your instructions" {
			t.Errorf("the user said %q", m.Text)
		}
	}
}

// The memory directory, and its index as it is, are in the instructions.
func TestMemoryInstructions(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "MEMORY.md"), []byte("- [tests](tests.md): run them with make\n"), 0o644)
	in := instructions(agent.StartSpec{Pack: agent.Pack{Text: "Be brief."}, Memory: dir})
	for _, want := range []string{"Be brief.", "Your memory directory is " + dir, "- [tests](tests.md): run them with make"} {
		if !strings.Contains(in, want) {
			t.Errorf("instructions = %q, want %q", in, want)
		}
	}
	if in := instructions(agent.StartSpec{}); in != "" {
		t.Errorf("no pack and no memory: %q", in)
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

// A session that can't start for want of a login says so as AuthExpired, with the command that signs in.
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
	if in.Version != "0.163.0" || !in.LoggedIn || in.Account != "chatgpt" || in.Note != "" {
		t.Errorf("install = %+v", in)
	}
	models, err := p.Models(context.Background())
	if err != nil || len(models) != 2 || models[0].ID != "gpt-fake-codex" || !models[0].Default || strings.Join(models[0].Efforts, ",") != "low,high" {
		t.Errorf("models = %+v, %v", models, err)
	}
	// model/list says no context window and no size: the catalog's model takes them from there.
	if m := models[1]; m.ID != "gpt-6-luna" || !m.Small || m.Context != 272_000 || models[0].Small || models[0].Context != 0 {
		t.Errorf("models = %+v", models)
	}
	u, err := p.Usage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if u.Plan != "ChatGPT Pro" || u.Tier != "Pro" || len(u.Windows) != 2 || u.Windows[0].Name != "Current session" || u.Windows[0].Used != 0.12 ||
		u.Windows[1].Name != "Current week" || !u.Windows[1].Resets.Equal(time.Unix(1700500000, 0)) {
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
	want := core.ProviderUsage{Provider: "codex", Plan: "ChatGPT Pro Lite", Tier: "Pro Lite", Credits: "12.50", Sampled: now, Source: "Codex's account/rateLimits/read", Windows: []core.UsageWindow{
		{Name: "Current session", Used: 0.5, Resets: time.Unix(100, 0), Length: 5 * time.Hour},
		{Name: "Current week", Used: 0.05, Resets: time.Unix(200, 0), Length: 7 * 24 * time.Hour},
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
	if b := bare.usage(now); b.Windows[0].Name != "Current session" || b.Windows[1].Name != "Current week" || b.Credits != "unlimited" || b.Plan != "" || b.Tier != "" {
		t.Errorf("bare = %+v", b)
	}
	if n := windowName(new(int64(1440)), 0); n != "1d" {
		t.Errorf("a day is %q", n)
	}
}

// Outside a sandbox every command asks ("untrusted"); a light session accepts its edits itself (TestLightPolicy).
//
// ref:   codex/codex-rs/protocol/src/protocol.rs
// quote: "Internal policy for projects marked untrusted. Commands require"
// ref:   codex/codex-rs/protocol/src/protocol.rs
// quote: "Never ask the user to approve commands. Failures are immediately returned"
func TestGate(t *testing.T) {
	for _, tc := range []struct {
		policy            agent.Policy
		sandboxed         bool
		approval, sandbox string
		fails             bool
	}{
		{agent.PolicyAsk, false, "untrusted", "workspace-write", false},
		{"", false, "untrusted", "workspace-write", false},
		{agent.PolicyLight, false, "untrusted", "workspace-write", false},
		{agent.PolicyBypass, false, "", "", true},
		{agent.PolicyBypass, true, "never", "danger-full-access", false},
		{agent.PolicyLight, true, "never", "danger-full-access", false},
		{agent.PolicyAsk, true, "never", "danger-full-access", false},
		// Refuse never asks and only reads, sandboxed or not.
		{agent.PolicyRefuse, false, "never", "read-only", false},
		{agent.PolicyRefuse, true, "never", "read-only", false},
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

// The MCP server answers initialize itself and passes tools to the session's socket.
func TestMCPServer(t *testing.T) {
	tools, err := serveTools(func() []agent.Tool {
		return []agent.Tool{agenttest.Echo, {Name: "fail", ReadOnly: true, Call: func(context.Context, json.RawMessage) (string, error) {
			return "", io.ErrUnexpectedEOF
		}}}
	})
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

// lastTurnEnd is the last TurnEnd s has said.
func lastTurnEnd(s *agenttest.Session) agent.TurnEnd {
	var end agent.TurnEnd
	for _, ev := range s.Events {
		if e, ok := ev.(agent.TurnEnd); ok {
			end = e
		}
	}
	return end
}

// The background terminals a turn leaves running end it as Running, not as Background: codex's don't wake the
// model. Every page of the list is read, and a terminal stops by its process id.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs
// quote: "pub struct ThreadBackgroundTerminal { pub item_id: String, pub process_id: String, pub command: String, pub cwd: LegacyAppPathString, pub os_pid: Option<u32>, pub cpu_percent: Option<f64>, pub rss_kb: Option<u64>, }"
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs
// quote: "/// Opaque cursor to pass to the next call to continue after the last item. /// If None, there are no more items to return. pub next_cursor: Option<String>,"
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/thread.rs
// quote: "pub struct ThreadBackgroundTerminalsTerminateParams { pub thread_id: String, pub process_id: String, }"
func TestBackgroundTerminalsAreRunning(t *testing.T) {
	if !fakeProvider(t).Features().StopProcesses {
		t.Error("codex doesn't say it stops processes")
	}
	h := agenttest.Harness{Provider: fakeProvider(t), Spec: fakeSpec, Timeout: 20 * time.Second}
	s := agenttest.Start(t, h)
	s.Send("start the dev server")
	s.Reply()
	end := lastTurnEnd(s)
	want := []core.BackgroundTask{
		{ID: "1001", Kind: "terminal", Description: "npm run dev", PID: 4242, CPU: 1.5, Memory: 2048 << 10},
		{ID: "1002", Kind: "terminal", Description: "go run ./watch"},
	}
	if !slices.Equal(end.Running, want) || len(end.Background) != 0 {
		t.Fatalf("running %+v, background %+v; want running %+v", end.Running, end.Background, want)
	}
	ps := s.S.(agent.ProcessStopper)
	if err := ps.StopProcess(context.Background(), "1001"); err != nil {
		t.Fatal(err)
	}
	if err := ps.StopProcess(context.Background(), "1001"); err == nil {
		t.Error("stopping a process that's gone said nothing")
	}
	s.Send("say hello")
	s.Reply()
	if got := lastTurnEnd(s).Running; !slices.Equal(got, want[1:]) {
		t.Errorf("after stopping 1001, running %+v; want %+v", got, want[1:])
	}
}

// A codex without the experimental background terminal methods ends its turns with nothing running, and says why
// it can't stop one.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/common.rs
// quote: "#[experimental(\"thread/backgroundTerminals/list\")] ThreadBackgroundTerminalsList => \"thread/backgroundTerminals/list\" {"
func TestNoBackgroundTerminalsOnAnOlderCodex(t *testing.T) {
	h := agenttest.Harness{Provider: fakeProvider(t), Spec: func(t *testing.T, dir string) agent.StartSpec {
		sp := fakeSpec(t, dir)
		sp.Env = append(sp.Env, "FAKE_CODEX_NO_TERMINALS=1")
		return sp
	}, Timeout: 20 * time.Second}
	s := agenttest.Start(t, h)
	s.Send("start the dev server")
	if got := s.Reply(); got != "the dev server is running" {
		t.Errorf("reply = %q", got)
	}
	if end := lastTurnEnd(s); len(end.Running) != 0 {
		t.Errorf("running %+v, want none", end.Running)
	}
	if err := s.S.(agent.ProcessStopper).StopProcess(context.Background(), "1001"); err == nil || !strings.Contains(err.Error(), "update codex") {
		t.Errorf("stop = %v", err)
	}
}
