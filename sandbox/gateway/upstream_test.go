package gateway

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/sandbox/mcp"
)

// installs is a provider with MCP servers installed for it.
type installs struct {
	*agenttest.Fake
	servers []agent.MCPServer
}

func (installs) SandboxNeeds(context.Context, agent.SandboxPlace) (agent.SandboxNeeds, error) {
	return agent.SandboxNeeds{}, nil
}

func (i installs) MCPServers() []agent.MCPServer { return i.servers }

func provider(id string, servers ...agent.MCPServer) agent.Provider {
	f := agenttest.NewFake()
	f.Name = id
	return installs{Fake: f, servers: servers}
}

// Found carries every provider's installed servers, whichever agent asks, and Oriel's own list over an installed
// server of the same name.
func TestFoundCarriesEveryProvidersServers(t *testing.T) {
	providers := []agent.Provider{
		provider("claude", agent.MCPServer{Name: "browser", Source: "user", Config: json.RawMessage(`{"command":"browser-mcp"}`)},
			agent.MCPServer{Name: "docs", Source: "user", Config: json.RawMessage(`{"command":"installed-docs"}`)}),
		agenttest.NewFake(),
		provider("codex", agent.MCPServer{Name: "notes", Source: "codex", Config: json.RawMessage(`{"command":"notes-mcp"}`)},
			agent.MCPServer{Name: "browser", Source: "codex", Config: json.RawMessage(`{"command":"other-browser"}`)}),
	}
	extra := map[string]json.RawMessage{"docs": json.RawMessage(`{"type":"http","url":"https://docs.example.com/mcp"}`)}
	got := map[string]Server{}
	var names []string
	for _, s := range Found(providers, extra, []string{"notes"}) {
		got[s.Name] = s
		names = append(names, s.Name)
	}
	if want := []string{"docs", "browser"}; !slices.Equal(names, want) {
		t.Fatalf("found %v, want %v", names, want)
	}
	if got["docs"].Source != "oriel" || got["docs"].URL != "https://docs.example.com/mcp" {
		t.Errorf("sandbox.servers' docs lost to the installed one: %+v", got["docs"])
	}
	if got["browser"].Command != "browser-mcp" {
		t.Errorf("the first provider's browser should win among installed: %+v", got["browser"])
	}
}

// startCounted is the fake server, noting each start of its process in the file it returns.
func startCounted(t *testing.T, name string) (Server, func() int) {
	s := fakeServer(t, name)
	path := filepath.Join(t.TempDir(), "starts")
	s.Env["GATEWAY_FAKE_STARTS"] = path
	return s, func() int {
		b, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			return 0
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Count(string(b), "started\n")
	}
}

func listNames(t *testing.T, u *Upstreams) []string {
	t.Helper()
	tools, errs := u.Tools(context.Background())
	if len(errs) > 0 {
		t.Fatalf("listing: %v", errs)
	}
	var out []string
	for _, x := range tools {
		out = append(out, x.Name)
	}
	slices.Sort(out)
	return out
}

func upstreams(t *testing.T, cache string, servers ...Server) *Upstreams {
	u := &Upstreams{Servers: servers, Cache: cache}
	t.Cleanup(u.Close)
	return u
}

// A server is started to list its tools only while there is no list of them kept; with one, listing starts nothing
// and the first call starts it.
func TestListingFromTheCacheStartsNoServer(t *testing.T) {
	cache := t.TempDir()
	s, starts := startCounted(t, "aws")
	want := []string{"aws__delete_bucket", "aws__list_buckets", "aws__put_object"}

	first := upstreams(t, cache, s)
	if got := listNames(t, first); !slices.Equal(got, want) || starts() != 1 {
		t.Fatalf("a cold list: %v, %d starts", got, starts())
	}
	first.Close()

	warm := upstreams(t, cache, s)
	if got := listNames(t, warm); !slices.Equal(got, want) {
		t.Fatalf("a warm list: %v", got)
	}
	if n := starts(); n != 1 {
		t.Fatalf("a warm list started the server: %d starts", n)
	}
	res, err := warm.Call(context.Background(), Tool{Server: "aws", Upstream: "put_object"}, json.RawMessage(`{"k":1}`))
	if err != nil || res.IsError || !strings.Contains(res.Text(), "ran put_object") {
		t.Fatalf("call: %+v %v", res, err)
	}
	if n := starts(); n != 2 {
		t.Fatalf("the call should start the server once: %d starts", n)
	}
	warm.Call(context.Background(), Tool{Server: "aws", Upstream: "list_buckets"}, nil)
	if n := starts(); n != 2 {
		t.Fatalf("a second call started it again: %d starts", n)
	}
}

// A server whose config changed is listed afresh, not from the list kept for the old one.
func TestAChangedConfigListsAgain(t *testing.T) {
	cache := t.TempDir()
	s, starts := startCounted(t, "aws")
	listNames(t, upstreams(t, cache, s))
	s.Env["REGION"] = "eu"
	listNames(t, upstreams(t, cache, s))
	if n := starts(); n != 2 {
		t.Fatalf("a changed config was listed from the cache: %d starts", n)
	}
	s.Env["REGION"] = "eu"
	listNames(t, upstreams(t, cache, s))
	if n := starts(); n != 2 {
		t.Fatalf("the same config was listed again: %d starts", n)
	}
}

// The kept list holds the tools and nothing of the server's config, for the owner alone.
func TestTheCacheHoldsNoSecrets(t *testing.T) {
	cache := t.TempDir()
	s, _ := startCounted(t, "aws")
	s.Env["AWS_SECRET"] = "hunter2"
	listNames(t, upstreams(t, cache, s))
	files, _ := filepath.Glob(filepath.Join(cache, "*"))
	if len(files) != 1 {
		t.Fatalf("cache files: %v", files)
	}
	b, _ := os.ReadFile(files[0])
	if strings.Contains(string(b), "hunter2") || strings.Contains(string(b), s.Command) || !strings.Contains(string(b), "list_buckets") {
		t.Errorf("the cache holds %s", b)
	}
	if fi, _ := os.Stat(files[0]); fi.Mode().Perm() != 0o600 {
		t.Errorf("the cache is %v", fi.Mode().Perm())
	}
}

// A call refreshes the kept list from the server: a tool it no longer has is an error for the agent, drops out of
// the list, and the list's version moves so the agent hears of it.
func TestACallRefreshesTheCache(t *testing.T) {
	cache := t.TempDir()
	s, _ := startCounted(t, "aws")
	listNames(t, upstreams(t, cache, s))
	stale := []mcp.Tool{{Name: "gone", InputSchema: json.RawMessage(`{"type":"object"}`)}, {Name: "list_buckets", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	(&Upstreams{Cache: cache}).keep(s, stale)

	u := upstreams(t, cache, s)
	if got := listNames(t, u); !slices.Equal(got, []string{"aws__gone", "aws__list_buckets"}) {
		t.Fatalf("the stale list: %v", got)
	}
	v := u.Version()
	res, err := u.Call(context.Background(), Tool{Server: "aws", Upstream: "gone"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Text(), "aws has no tool called gone") {
		t.Errorf("calling a tool the server dropped: %+v", res)
	}
	if got := listNames(t, u); slices.Contains(got, "aws__gone") || !slices.Contains(got, "aws__put_object") {
		t.Errorf("the list after the call: %v", got)
	}
	if u.Version() == v {
		t.Error("the tool list changed but its version didn't")
	}
	if got := listNames(t, upstreams(t, cache, s)); slices.Contains(got, "aws__gone") {
		t.Errorf("the cache kept the dropped tool: %v", got)
	}
}
