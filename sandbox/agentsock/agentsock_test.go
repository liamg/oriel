package agentsock

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/sandbox/egress"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/sandbox/mcp"
)

// serve runs h on a socket under a short temp dir and returns its path.
func serve(t *testing.T, h *Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "oas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "oriel.sock")
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = Serve(ctx, h, path, filepath.Join(dir, "port")) }()
	for range 200 {
		if _, err := os.Stat(path); err == nil {
			return path
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("socket never opened")
	return ""
}

func handler(t *testing.T) *Handler {
	g := &gateway.Gateway{Builtins: map[string]gateway.Builtin{
		"oriel__ci": {Tool: mcp.Tool{Name: "oriel__ci"}, Run: func(context.Context, json.RawMessage) (mcp.CallResult, error) {
			return mcp.TextResult("green", false), nil
		}},
	}}
	return &Handler{
		Token:        "tok",
		Gateway:      g,
		Egress:       &egress.Session{GrantsPath: filepath.Join(t.TempDir(), "egress.json")},
		SSH:          &SSHAgent{},
		ToolsVersion: func() int { return 3 },
	}
}

func TestClientReachesTheGateway(t *testing.T) {
	path := serve(t, handler(t))
	c := NewSocket(path, "tok")
	ctx := t.Context()
	tools, v, err := c.GatewayTools(ctx)
	if err != nil || len(tools) != 1 || v != 3 {
		t.Fatalf("tools %s v%d %v", tools, v, err)
	}
	raw, err := c.GatewayCall(ctx, "oriel__ci", nil)
	var res mcp.CallResult
	if err != nil || json.Unmarshal(raw, &res) != nil || res.Text() != "green" {
		t.Fatalf("call %s %v", raw, err)
	}
	if ok, _, err := c.EgressDecide(ctx, "api.anthropic.com", 443); err != nil || !ok {
		t.Fatalf("decide %v %v", ok, err)
	}
	if ok, why, err := c.EgressDecide(ctx, "nowhere.example.com", 443); err != nil || ok || why != "nobody answered the card" {
		t.Fatalf("no approver should refuse, saying why: %v %q %v", ok, why, err)
	}
	if live, err := c.SSHStatus(ctx); err != nil || live {
		t.Fatalf("ssh %v %v", live, err)
	}
}

func TestWrongTokenIsRefused(t *testing.T) {
	path := serve(t, handler(t))
	_, _, err := NewSocket(path, "nope").GatewayTools(t.Context())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("got %v, want 401", err)
	}
}

func TestEgressLogTakesTheSessionsReason(t *testing.T) {
	h := handler(t)
	c := NewSocket(serve(t, h), "tok")
	if _, _, err := c.EgressDecide(t.Context(), "github.com", 443); err != nil {
		t.Fatal(err)
	}
	if err := c.EgressLog(egress.Event{Host: "github.com", Port: 443, Allowed: true, Reason: "trust me"}); err != nil {
		t.Fatal(err)
	}
	if got := h.Egress.Conns(); len(got) != 1 || got[0].Reason != "rule: built-in list" {
		t.Fatalf("conns %+v", got)
	}
}
