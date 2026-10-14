package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/sandbox/mcp"
)

// A fake upstream: the test binary re-executed as a stdio MCP server.
func TestMain(m *testing.M) {
	if v := os.Getenv("GATEWAY_FAKE_MCP"); v != "" {
		srv := &mcp.Server{Name: "fake", Handler: fakeHandler{github: v == "github"}}
		_ = srv.Serve(context.Background(), os.Stdin, os.Stdout)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type fakeHandler struct{ github bool }

func (f fakeHandler) Tools(context.Context) ([]mcp.Tool, error) {
	yes := true
	schema := json.RawMessage(`{"type":"object"}`)
	if f.github {
		return []mcp.Tool{
			{Name: "pull_request_read", InputSchema: schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: &yes}},
			{Name: "add_reply_to_pull_request_comment", InputSchema: schema},
			{Name: "pull_request_review_write", InputSchema: schema},
		}, nil
	}
	return []mcp.Tool{
		{Name: "list_buckets", InputSchema: schema, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: &yes}},
		{Name: "delete_bucket", InputSchema: schema},
		{Name: "put_object", InputSchema: schema},
	}, nil
}

func (fakeHandler) Call(_ context.Context, name string, args json.RawMessage) (mcp.CallResult, error) {
	return mcp.TextResult("ran "+name+" "+string(args), false), nil
}

func fakeServer(t *testing.T, name string) Server {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Server{Name: name, Type: "stdio", Command: exe, Env: map[string]string{"GATEWAY_FAKE_MCP": "1"}}
}

// harness is a gateway with a scripted user.
type harness struct {
	g       *Gateway
	mu      sync.Mutex
	session []Rule
	global  []Rule
	asked   []Request
	answer  Answer
}

func newHarness(t *testing.T) *harness {
	h := &harness{}
	up := &Upstreams{Servers: []Server{fakeServer(t, "aws")}}
	t.Cleanup(up.Close)
	h.g = &Gateway{
		Builtins: map[string]Builtin{
			"oriel__push": {Tool: mcp.Tool{Name: "oriel__push"}, Run: func(context.Context, json.RawMessage) (mcp.CallResult, error) {
				return mcp.TextResult("pushed", false), nil
			}},
			"oriel__ci": {Tool: mcp.Tool{Name: "oriel__ci"}, Run: func(context.Context, json.RawMessage) (mcp.CallResult, error) {
				return mcp.TextResult("green", false), nil
			}},
			"oriel__ask_user_to_run": {Tool: mcp.Tool{Name: "oriel__ask_user_to_run"}, AlwaysAsk: true, Run: func(context.Context, json.RawMessage) (mcp.CallResult, error) {
				return mcp.TextResult("ran", false), nil
			}},
		},
		Up: up,
		Levels: func() Levels {
			h.mu.Lock()
			defer h.mu.Unlock()
			return Levels{Session: append([]Rule(nil), h.session...), Global: append([]Rule(nil), h.global...)}
		},
		Approve: func(_ context.Context, r Request) (Answer, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.asked = append(h.asked, r)
			return h.answer, nil
		},
		Grant: func(rules []Rule) {
			h.mu.Lock()
			h.session = append(h.session, rules...)
			h.mu.Unlock()
		},
		Always: func(rules []Rule, _ bool) {
			h.mu.Lock()
			h.global = append(h.global, rules...)
			h.mu.Unlock()
		},
	}
	return h
}

func (h *harness) call(t *testing.T, name string) (string, bool) {
	t.Helper()
	res, err := h.g.Call(context.Background(), name, json.RawMessage(`{"a":1}`))
	if err != nil {
		t.Fatal(err)
	}
	return res.Text(), !res.IsError
}

func (h *harness) asks() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.asked)
}

func TestAskOnceSessionDeny(t *testing.T) {
	h := newHarness(t)

	// Allowed by default: no card.
	if out, ok := h.call(t, "oriel__ci"); !ok || out != "green" || h.asks() != 0 {
		t.Fatalf("ci: %q %v asks=%d", out, ok, h.asks())
	}

	// Once: runs, asks again next time.
	h.answer = Answer{Decision: Once}
	if out, ok := h.call(t, "aws__put_object"); !ok || !strings.Contains(out, "ran put_object") {
		t.Fatalf("once: %q %v", out, ok)
	}
	h.call(t, "aws__put_object")
	if h.asks() != 2 {
		t.Fatalf("once should ask every time, asked %d", h.asks())
	}
	req := h.asked[0]
	if req.Server != "aws" || len(req.Siblings) != 3 || string(req.Args) != `{"a":1}` {
		t.Errorf("card request = %+v", req)
	}

	// Session: asked once, then allowed.
	h.answer = Answer{Decision: Session}
	h.call(t, "oriel__push")
	h.call(t, "oriel__push")
	if h.asks() != 3 {
		t.Fatalf("session should ask once, asked %d total", h.asks())
	}

	// Deny carries the note back to the agent.
	h.answer = Answer{Decision: Refuse, Note: "not today"}
	out, ok := h.call(t, "aws__delete_bucket")
	if ok || !strings.Contains(out, "not today") {
		t.Fatalf("deny: %q %v", out, ok)
	}
}

func TestServerGrantWithException(t *testing.T) {
	h := newHarness(t)
	h.global = []Rule{{Tool: "aws__delete_*", Action: Ask}}
	h.answer = Answer{Decision: Session, Server: true}
	h.call(t, "aws__list_buckets")
	if h.asks() != 1 {
		t.Fatalf("asked %d", h.asks())
	}
	h.call(t, "aws__put_object")
	if h.asks() != 1 {
		t.Fatalf("server grant should cover put_object; asked %d", h.asks())
	}
	// The settings' ask on deletes is more specific than the server grant.
	h.answer = Answer{Decision: Once}
	h.call(t, "aws__delete_bucket")
	if h.asks() != 2 {
		t.Fatalf("delete should still be asked about, asked %d", h.asks())
	}
}

func TestSettingsDenyBeatsSessionGrant(t *testing.T) {
	h := newHarness(t)
	h.session = []Rule{{Tool: "aws__*", Action: Allow}}
	h.global = []Rule{{Tool: "aws__delete_bucket", Action: Deny}}
	if _, ok := h.call(t, "aws__delete_bucket"); ok {
		t.Fatal("a settings deny must beat a session grant")
	}
	tools, _ := h.g.Tools(context.Background())
	for _, tl := range tools {
		if tl.Name == "aws__delete_bucket" {
			t.Error("a denied tool is still listed")
		}
	}
}

// RV-4: "the agent never posts": a tool that posts to a GitHub review isn't offered, and a call to it is refused
// even when the user's rules allow every tool, without asking.
func TestReviewPostingToolsAreNeverOffered(t *testing.T) {
	h := newHarness(t)
	gh := fakeServer(t, "github")
	gh.Env["GATEWAY_FAKE_MCP"] = "github"
	h.g.Up = &Upstreams{Servers: []Server{gh}}
	t.Cleanup(h.g.Up.Close)
	h.session = []Rule{{Tool: "*", Action: Allow}}
	h.global = []Rule{{Tool: "github__*", Action: Allow}}
	tools, _ := h.g.Tools(context.Background())
	var names []string
	for _, tl := range tools {
		names = append(names, tl.Name)
	}
	if strings.Contains(strings.Join(names, " "), "add_reply_to_pull_request_comment") || strings.Contains(strings.Join(names, " "), "pull_request_review_write") ||
		!strings.Contains(strings.Join(names, " "), "github__pull_request_read") {
		t.Errorf("offered %q", names)
	}
	if out, ok := h.call(t, "github__add_reply_to_pull_request_comment"); ok || h.asks() != 0 {
		t.Errorf("a review reply ran: %q, asked %d", out, h.asks())
	}
	for _, p := range h.g.Policy(context.Background()) {
		if p.Name == "github__pull_request_review_write" && (p.Action != Deny || p.From != FromOriel) {
			t.Errorf("policy shows %+v", p)
		}
	}
}

func TestAlwaysAskIgnoresGrants(t *testing.T) {
	h := newHarness(t)
	h.global = []Rule{{Tool: "*", Action: Allow}}
	h.answer = Answer{Decision: Session, Always: true}
	h.call(t, "oriel__ask_user_to_run")
	h.call(t, "oriel__ask_user_to_run")
	if h.asks() != 2 {
		t.Fatalf("ask_user_to_run must always ask, asked %d", h.asks())
	}
}

func TestEverywhereReachesTheStore(t *testing.T) {
	h := newHarness(t)
	var got bool
	h.g.Always = func(_ []Rule, everywhere bool) { got = everywhere }
	h.answer = Answer{Decision: Once, Everywhere: true}
	h.call(t, "aws__put_object")
	if !got {
		t.Fatal("everywhere was not passed on")
	}
}

func TestAlwaysWritesGlobalRule(t *testing.T) {
	h := newHarness(t)
	h.answer = Answer{Decision: Once, Always: true, Tools: []string{"aws__list_buckets"}}
	h.call(t, "aws__put_object")
	if len(h.global) != 2 {
		t.Fatalf("global rules = %+v", h.global)
	}
}

func TestDecideSpecificity(t *testing.T) {
	l := Levels{Global: []Rule{
		{Tool: "aws__*", Action: Allow},
		{Tool: "aws__delete_*", Action: Ask},
		{Tool: "aws__delete_everything", Action: Deny},
	}}
	for tool, want := range map[string]string{
		"aws__list":              Allow,
		"aws__delete_bucket":     Ask,
		"aws__delete_everything": Deny,
		"linear__create":         Ask,
		"oriel__ci":              Allow,
	} {
		if got := Decide(tool, false, l); got != want {
			t.Errorf("%s: got %s want %s", tool, got, want)
		}
	}
	if got := Decide("aws__x", true, Levels{ReadOnly: true}); got != Allow {
		t.Errorf("readOnlyHint with ReadOnly: %s", got)
	}
}

func TestDiscover(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	write := func(p, body string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plug := filepath.Join(home, ".claude", "plugins", "cache", "linear")
	write(filepath.Join(home, ".claude.json"), `{"mcpServers":{"aws":{"command":"aws-mcp","env":{"R":"${REGION:-eu}"}},"old":{"type":"sse","url":"x"}},
		"projects":{"/x":{"mcpServers":{"repo":{"command":"evil"}}}}}`)
	write(filepath.Join(home, ".claude", "settings.json"), `{"enabledPlugins":{"linear@official":true,"off@official":false}}`)
	write(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"),
		`{"plugins":{"linear@official":[{"installPath":"`+plug+`"}],"off@official":[{"installPath":"/nope"}]}}`)
	write(filepath.Join(plug, ".mcp.json"), `{"linear":{"type":"http","url":"https://mcp.linear.app/mcp"}}`)

	got := Discover(home, map[string]json.RawMessage{"extra": json.RawMessage(`{"command":"x"}`)}, []string{"hiddenone"})
	var names []string
	for _, s := range got {
		names = append(names, s.Name+"/"+s.Source)
	}
	want := "aws/user linear/plugin:linear extra/oriel"
	if strings.Join(names, " ") != want {
		t.Fatalf("got %v want %s", names, want)
	}
	if got[0].Env["R"] != "eu" {
		t.Errorf("env default not expanded: %v", got[0].Env)
	}
	if got[1].URL != "https://mcp.linear.app/mcp" {
		t.Errorf("plugin url = %q", got[1].URL)
	}
}

func TestToolNameFits(t *testing.T) {
	n := ToolName("averyveryverylongservername", strings.Repeat("x", 80))
	if len(n) > 64 || !strings.HasPrefix(n, "averyveryverylongservername__") {
		t.Errorf("ToolName = %q (%d)", n, len(n))
	}
}

func TestExplainSaysWhichRuleDecided(t *testing.T) {
	l := Levels{
		Session:  []Rule{{Tool: "aws__*", Action: Allow}},
		Project:  []Rule{{Tool: "notes__secret", Action: Deny}},
		Global:   []Rule{{Tool: "aws__delete_*", Action: Ask}, {Tool: "linear__*", Action: Allow}},
		ReadOnly: true,
	}
	for _, c := range []struct {
		tool string
		ro   bool
		want Why
	}{
		{"aws__list", false, Why{Allow, FromSession, "aws__*"}},
		{"aws__delete_bucket", false, Why{Ask, FromGlobal, "aws__delete_*"}},
		{"notes__secret", false, Why{Deny, FromProject, "notes__secret"}},
		{"linear__get", false, Why{Allow, FromGlobal, "linear__*"}},
		{"docs__read", true, Why{Action: Allow, From: FromReadOnly}},
		{"docs__write", false, Why{Ask, FromDefault, "*"}},
		{"oriel__ci", false, Why{Allow, FromDefault, "oriel__ci"}},
	} {
		if got := Explain(c.tool, c.ro, l); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.tool, got, c.want)
		}
		if got := Decide(c.tool, c.ro, l); got != c.want.Action {
			t.Errorf("%s: Decide = %s", c.tool, got)
		}
	}
}
