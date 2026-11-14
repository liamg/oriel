package codex

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/semver"
)

// A subagent the agent spawns is a Subagent, what its thread does is its, and a collab call's last state finishes it.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/item.rs
// quote: "Thread ID of the receiving agent, when applicable. In case of spawn operation,"
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/item.rs
// quote: "Last known status of the target agents, when available. agents_states: HashMap<String, CollabAgentState>,"
func TestSubagents(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w"})
	w.notify("item/started", `{"threadId":"th","turnId":"tu","item":{"type":"collabAgentToolCall","id":"c1","tool":"spawnAgent",
		"status":"inProgress","senderThreadId":"th","receiverThreadIds":["kid"],"prompt":"find the flaky test\nthen fix it","agentsStates":{}}}`)
	if c := w.next().(agent.ToolCall); c.Tool != "Agent" || !strings.Contains(string(c.Input), `"description":"find the flaky test"`) {
		t.Errorf("call = %+v", c)
	}
	if a := w.next().(agent.Subagent); a.ID != "kid" || a.Description != "find the flaky test" || a.Done {
		t.Errorf("subagent = %+v", a)
	}
	w.notify("item/completed", `{"threadId":"kid","turnId":"k1","item":{"type":"agentMessage","id":"km","text":"looking"}}`)
	if m := w.next().(agent.Message); m.Agent != "kid" || m.Text != "looking" {
		t.Errorf("the subagent's message = %+v", m)
	}
	w.notify("item/completed", `{"threadId":"th","turnId":"tu","item":{"type":"collabAgentToolCall","id":"c2","tool":"wait",
		"status":"completed","senderThreadId":"th","receiverThreadIds":["kid"],"prompt":null,
		"agentsStates":{"kid":{"status":"completed","message":"found it"}}}}`)
	if a := w.next().(agent.Subagent); a.ID != "kid" || !a.Done || a.Result != "found it" {
		t.Errorf("subagent = %+v", a)
	}
}

// codex saying it's signed in, or a sign-in completing, is a login seen back.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/common.rs
// quote: "AccountUpdated => \"account/updated\" (v2::AccountUpdatedNotification),"
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/account.rs
// quote: "pub struct AccountUpdatedNotification { pub auth_mode: Option<AuthMode>,"
func TestAuthRestored(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w"})
	lose := func() {
		w.notify("error", `{"threadId":"th","turnId":"tu","error":{"message":"401","codexErrorInfo":"unauthorized"},"willRetry":false}`)
		w.next()
		if _, ok := w.next().(agent.AuthExpired); !ok {
			t.Fatal("no AuthExpired")
		}
	}
	// Every connection hears account/updated as it starts: that's no login back, as its token may be the one refused.
	w.notify("account/updated", `{"authMode":"chatgpt","planType":"pro"}`)
	lose()
	w.notify("account/updated", `{"authMode":null,"planType":null}`)
	w.notify("account/updated", `{"authMode":"chatgpt","planType":"pro"}`)
	if ev, ok := w.next().(agent.AuthRestored); !ok || ev.Provider != "codex" {
		t.Errorf("event = %+v", ev)
	}
	w.notify("account/login/completed", `{"loginId":"l","success":true,"error":null}`)
	if _, ok := w.next().(agent.AuthRestored); !ok {
		t.Error("a completed sign-in wasn't a login back")
	}
	// A turn that works after the login went is the login back.
	lose()
	w.notify("turn/completed", `{"threadId":"th","turn":{"id":"tu","status":"failed","items":[]}}`)
	w.next()
	w.notify("turn/completed", `{"threadId":"th","turn":{"id":"t2","status":"completed","items":[]}}`)
	if _, ok := w.next().(agent.AuthRestored); !ok {
		t.Error("a turn that worked after the login went wasn't a login back")
	}
}

// An interrupt as soon as turn/start answers stops the turn it started, before turn/started is heard.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/common.rs
// quote: "TurnInterrupt => \"turn/interrupt\" {"
func TestInterruptAsTheTurnStarts(t *testing.T) {
	s := agenttest.Start(t, agenttest.Harness{Provider: fakeProvider(t), Spec: fakeSpec, Timeout: 20 * time.Second})
	for range 5 {
		s.Send("work slowly")
		if err := s.S.Interrupt(context.Background()); err != nil {
			t.Fatal(err)
		}
		s.Wait("the turn to end", func(ev agent.Event) bool { _, ok := ev.(agent.TurnEnd); return ok })
	}
}

// With the user's MCP servers left out (no MCP, or a sandbox's gateway), each codex's config has is turned off, as
// are ChatGPT's apps; Oriel's and the gateway's stay.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/common.rs
// quote: "ConfigRead => \"config/read\" {"
// ref:   codex/codex-rs/core/config.schema.json
// quote: "\"disabled_tools\": { \"default\": null, \"items\": { \"type\": \"string\" }, \"type\": \"array\" }, \"enabled\": { \"default\": null, \"type\": \"boolean\" },"
func TestStrictMCP(t *testing.T) {
	w := wired(t, agent.StartSpec{Cwd: "/w", NoMCP: true})
	w.mu.Lock()
	w.reads = map[string]any{"config": map[string]any{"mcp_servers": map[string]any{"linear": map[string]any{"command": "l"},
		"github": map[string]any{"url": "https://gh"}}}, "origins": map[string]any{}}
	w.mu.Unlock()
	w.s.tools = &toolServer{path: "/tmp/s.sock"}
	user, err := w.s.userServers(context.Background())
	if err != nil || !slices.Equal(user, []string{"github", "linear"}) {
		t.Fatalf("user servers = %v, %v", user, err)
	}
	cfg, err := w.s.config(user)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["mcp_servers.github.enabled"] != false || cfg["mcp_servers.linear.enabled"] != false || cfg["features.apps"] != false || cfg["mcp_servers.oriel"] == nil {
		t.Errorf("config = %v", cfg)
	}
	loose := newSession(&Provider{}, agent.StartSpec{})
	defer loose.events.Close()
	loose.tools = w.s.tools
	if cfg, _ := loose.config(user); len(cfg) != 1 {
		t.Errorf("without strict, config = %v", cfg)
	}
}

// With NoWeb a session's web search, which OpenAI runs and nobody is asked about, is off; otherwise it's Codex's own.
//
// ref:   codex/codex-rs/core/config.schema.json
// quote: "Controls the web search tool mode: disabled, cached, indexed, or live."
func TestNoWebTurnsWebSearchOff(t *testing.T) {
	for _, no := range []bool{true, false} {
		s := newSession(&Provider{}, agent.StartSpec{NoWeb: no})
		s.tools = &toolServer{path: "/tmp/s.sock"}
		cfg, err := s.config(nil)
		s.events.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got, set := cfg["web_search"]; no && got != "disabled" || !no && set {
			t.Errorf("NoWeb %v: web_search = %v", no, got)
		}
	}
}

func TestGatewayConfig(t *testing.T) {
	f := filepath.Join(t.TempDir(), "gw.json")
	os.WriteFile(f, []byte(`{"mcpServers":{"gw":{"type":"http","url":"http://gw/mcp","headers":{"X":"y"}},"local":{"command":"x","args":["a"]}}}`), 0o644)
	s := newSession(&Provider{OrielBin: "/bin/oriel"}, agent.StartSpec{Sandbox: &agent.Sandbox{GatewayMCP: f, HookSocket: "/run/oriel/hook.sock"}})
	defer s.events.Close()
	s.tools = &toolServer{path: "/tmp/s.sock"}
	cfg, err := s.config([]string{"gw", "mine"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(cfg)
	for _, want := range []string{
		// In a sandbox, the oriel on its PATH, at the socket's path there.
		`"mcp_servers.oriel":{"args":["hook","mcp"],"command":"oriel","default_tools_approval_mode":"approve","env":{"ORIEL_HOOK_SOCK":"/run/oriel/hook.sock"}}`,
		`"mcp_servers.gw":{"http_headers":{"X":"y"},"url":"http://gw/mcp"}`,
		`"mcp_servers.local":{"args":["a"],"command":"x"}`,
		`"mcp_servers.mine.enabled":false`,
	} {
		if !strings.Contains(string(b), want) {
			t.Errorf("config = %s\nwant %s", b, want)
		}
	}
	if strings.Contains(string(b), "mcp_servers.gw.enabled") {
		t.Errorf("the gateway is turned off: %s", b)
	}
}

// The static models are the bundled catalog's listed ones, with its context windows and efforts; the fast,
// affordable one is the small model.
//
// ref:   codex/codex-rs/models-manager/models.json
// quote: "\"description\": \"Fast and affordable model for easier tasks.\","
func TestStaticModelsMatchCatalog(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "spec", "codex", "codex-rs", "models-manager", "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cat struct{ Models []catalogModel }
	if err := json.Unmarshal(b, &cat); err != nil {
		t.Fatal(err)
	}
	var listed []string
	slices.SortStableFunc(cat.Models, func(a, b catalogModel) int { return a.Priority - b.Priority })
	for _, m := range cat.Models {
		if m.Visibility != "list" {
			continue
		}
		listed = append(listed, m.Slug)
		i := slices.IndexFunc(staticModels, func(s agent.Model) bool { return s.ID == m.Slug })
		if i < 0 {
			t.Errorf("%s is listed in the catalog, not in staticModels", m.Slug)
			continue
		}
		s := staticModels[i]
		var efforts []string
		for _, l := range m.Levels {
			efforts = append(efforts, l.Effort)
		}
		small := m.Description == "Fast and affordable model for easier tasks."
		if s.Context != m.Context || !slices.Equal(s.Efforts, efforts) || s.Small != small || s.Name != m.DisplayName {
			t.Errorf("%s = %+v, the catalog's: %d %v small %v %q", m.Slug, s, m.Context, efforts, small, m.DisplayName)
		}
	}
	var ours []string
	for _, s := range staticModels {
		ours = append(ours, s.ID)
	}
	if !slices.Equal(ours, listed) || !staticModels[0].Default {
		t.Errorf("static models %v, the catalog lists %v", ours, listed)
	}
}

// catalogModel is a model as Codex's bundled catalog has it.
type catalogModel struct {
	Slug, Description, Visibility string
	DisplayName                   string `json:"display_name"`
	Context                       int    `json:"context_window"`
	Priority                      int
	Levels                        []struct{ Effort string } `json:"supported_reasoning_levels"`
}

// Not installed, codex is a note, its usage nothing to read, and its models the catalog's.
func TestNotInstalled(t *testing.T) {
	p := &Provider{Bin: filepath.Join(t.TempDir(), "codex")}
	in, err := p.Detect(context.Background())
	if err != nil || in.LoggedIn || !strings.Contains(in.Note, "not installed") {
		t.Errorf("install = %+v, %v", in, err)
	}
	if u, err := p.Usage(context.Background()); err != nil || len(u.Windows) != 0 {
		t.Errorf("usage = %+v, %v", u, err)
	}
	if ms, _ := p.Models(context.Background()); len(ms) != len(staticModels) {
		t.Errorf("models = %+v", ms)
	}
	if _, err := p.Start(context.Background(), agent.StartSpec{Cwd: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "isn't installed") {
		t.Errorf("start = %v", err)
	}
}

// A codex whose app-server won't come up is a usage probe that never started.
func TestUsageProbeFailed(t *testing.T) {
	bin, err := exec.LookPath("false")
	if err != nil {
		t.Skip("no false")
	}
	_, err = (&Provider{Bin: bin}).Usage(context.Background())
	var pf agent.UsageProbeFailed
	if !errors.As(err, &pf) || pf.Provider() != "codex" || pf.Started() {
		t.Errorf("err = %#v", err)
	}
}

// A codex older than the protocol Oriel speaks says so.
func TestTooOld(t *testing.T) {
	if !semver.Less("0.162.1", minVersion) || semver.Less("0.163.0", minVersion) || semver.Less("0.170.2", minVersion) {
		t.Errorf("minVersion %s", minVersion)
	}
}

// The sign-in's mark is its token's, so a new token is a new sign-in.
//
// ref:   codex/codex-rs/login/src/auth/storage.rs
// quote: "Expected structure for $CODEX_HOME/auth.json."
// ref:   codex/codex-rs/login/src/token_data.rs
// quote: "/// This is a JWT. pub access_token: String,"
func TestLoginMark(t *testing.T) {
	dir := t.TempDir()
	if m := loginMark(dir); m != "" {
		t.Errorf("no auth.json: %q", m)
	}
	write := func(token string) string {
		os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"OPENAI_API_KEY":null,"tokens":{"id_token":"x","access_token":"`+token+`","refresh_token":"r","account_id":null},"last_refresh":"2026-10-01T00:00:00Z"}`), 0o600)
		return loginMark(dir)
	}
	a, b := write("one"), write("two")
	if a == "" || b == "" || a == b || write("one") != a {
		t.Errorf("marks %q %q", a, b)
	}
}

// Detect marks the sign-in it found.
func TestDetectMarksTheLogin(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"tokens":{"access_token":"t"}}`), 0o600)
	in, err := fakeProvider(t).Detect(context.Background())
	if err != nil || in.Login == "" || in.Login != loginMark(dir) {
		t.Errorf("install = %+v, %v", in, err)
	}
}

// StartLogin is ChatGPT's browser sign-in: its URL, and how it ended.
//
// ref:   codex/codex-rs/app-server-protocol/src/protocol/v2/account.rs
// quote: "/// URL the client should open in a browser to initiate the OAuth flow. auth_url: String,"
// ref:   codex/codex-rs/app-server-protocol/src/protocol/common.rs
// quote: "#[serde(rename = \"account/login/completed\")]"
func TestStartLogin(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	l, err := fakeProvider(t).StartLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.URL, "https://auth.example/") {
		t.Errorf("url = %q", l.URL)
	}
	select {
	case err := <-l.Done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the sign-in never ended")
	}
	if f := (&Provider{}).Features(); !f.Login || !f.Images || f.Terminal || !f.Fork {
		t.Errorf("features = %+v", f)
	}
}

// Logs are the thread's rollouts.
func TestLogs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEX_HOME", dir)
	id := "0199a213-81c0-7800-8aa1-bbab2a035a53"
	p := filepath.Join(dir, "sessions/2026/10/01/rollout-2026-10-01T09-00-00-"+id+".jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("{}\n"), 0o644)
	logs := (&Provider{}).Logs("/w", id)
	if len(logs) != 1 || logs[0].Path != p || logs[0].Format != "codex" {
		t.Errorf("logs = %+v", logs)
	}
	if logs := (&Provider{}).Logs("/w", "other"); len(logs) != 0 {
		t.Errorf("another thread's logs = %+v", logs)
	}
}
