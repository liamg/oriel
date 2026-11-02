package settings_test

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
)

// The sandbox's settings are options of the right types, with the project layer only
// where a repo may set its own (its network and gateway rules, its image).
func TestSandboxOptionsAreDeclared(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	o := s.Registry().Options()
	for _, tt := range []struct {
		name    string
		typ     registry.OptionType
		def     any
		project bool
	}{
		{"sandbox.default", registry.Enum, "sandbox", false},
		{"sandbox.image", registry.String, "base", true},
		{"sandbox.env", registry.List, []string{}, false},
		{"sandbox.egress", registry.List, []string{}, true},
		{"sandbox.rules", registry.List, []string{}, true},
		{"sandbox.approval_timeout", registry.Duration, 10 * time.Minute, false},
		{"sandbox.auto_allow_read_only", registry.Bool, false, false},
		{"sandbox.hidden_servers", registry.List, []string{}, false},
		{"sandbox.egress_proxy", registry.Bool, true, false},
		{"sandbox.memory", registry.String, "8g", false},
		{"sandbox.pids_limit", registry.Int, 4096, false},
		{"sandbox.go_cache_max", registry.String, "20g", false},
		{"sandbox.servers", registry.Map, map[string]string{}, false},
	} {
		sp, ok := o.Spec(tt.name)
		if !ok {
			t.Errorf("%s isn't registered", tt.name)
			continue
		}
		if sp.Type != tt.typ || !reflect.DeepEqual(sp.Default, tt.def) || sp.Help == "" || sp.Scopes&registry.ScopeUser == 0 ||
			(sp.Scopes&registry.ScopeProject != 0) != tt.project {
			t.Errorf("%s = %+v", tt.name, sp)
		}
	}
	for _, name := range []string{"sandbox.env", "sandbox.hidden_servers"} {
		if sp, _ := o.Spec(name); sp.Choices == nil {
			t.Errorf("%s offers no choices", name)
		}
	}
	if sp, _ := o.Spec("sandbox.servers"); sp.KeyLabel != "name" || sp.ValueLabel != "server" {
		t.Errorf("sandbox.servers labels its sides %q and %q", sp.KeyLabel, sp.ValueLabel)
	}
	t.Setenv("ORIEL_TEST_VAR", "1")
	if sp, _ := o.Spec("sandbox.env"); !slices.Contains(sp.Choices(), "ORIEL_TEST_VAR") {
		t.Errorf("sandbox.env doesn't offer the host's variables: %v", sp.Choices())
	}
	for name, bad := range map[string]any{"sandbox.egress": []string{"allow"}, "sandbox.rules": []string{"always oriel__push"},
		"sandbox.memory": "lots", "sandbox.go_cache_max": "big", "sandbox.approval_timeout": -time.Second, "sandbox.servers": map[string]string{"Git Hub": "https://x"},
		"sandbox.servers ": map[string]string{"gh": `npx "open`}} {
		name = strings.TrimSpace(name)
		if err := o.Set(name, registry.LayerUser, registry.At{}, bad); err == nil {
			t.Errorf("%s took %v", name, bad)
		}
	}
}

// Your sandbox.* values, and each trusted project file's own, reach the host as the
// sandbox.Config its sandboxes run on; a repo a session works in brings its project file even when the window is
// elsewhere, and a change reaches the host again.
func TestSandboxSettingsReachTheHost(t *testing.T) {
	lw := rig.NewHost(t)
	other := filepath.Join(t.TempDir(), "billing")
	rig.GitRepo(t, other, map[string]string{"README.md": "billing\n"}, 1)
	s := apptest.New(t, apptest.Options{WorkDir: lw.Repo, Config: apptest.Connect(lw.W)})
	rig.WriteFile(t, filepath.Join(s.ConfigDir, "settings.toml"), `[sandbox]
memory = "4g"
go_cache_max = "8g"
egress = ["allow proxy.golang.org", "deny ads.example.com"]
approval_timeout = "2m"
egress_proxy = false
env = ["GOFLAGS"]

[sandbox.servers]
docs = "https://docs.example.com/mcp"
linear = "npx -y linear-mcp"
`)
	rig.WriteFile(t, filepath.Join(lw.Repo, ".oriel", "settings.toml"), "[sandbox]\negress = [\"allow registry.npmjs.org:443\"]\n")
	rig.WriteFile(t, filepath.Join(other, ".oriel", "settings.toml"), "[sandbox]\nrules = [\"deny oriel__push\"]\nimage = \"billing:dev\"\n")
	settingsOf(s).Reload()
	s.Run("settings.trust", registry.Args{"path": filepath.Join(lw.Repo, ".oriel", "settings.toml")})
	if _, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Repos: []core.RepoID{core.RepoID(other)}}); err != nil {
		t.Fatal(err)
	}
	rig.WaitState(t, s.Store(), "the session on billing", func(st core.State) bool { return len(st.Sessions) == 1 })
	settingsOf(s).Reload()
	s.Run("settings.trust", registry.Args{"path": filepath.Join(other, ".oriel", "settings.toml")})
	cfg := waitSandbox(t, lw, "the user's and both projects' settings", func(c sandbox.Config) bool {
		return len(c.Projects[other].Rules) == 1 && len(c.Projects[lw.Repo].Egress) == 1
	})
	want := sandbox.Config{Default: "sandbox", Backend: "docker", Loopback: "all", Memory: "4g", GoCacheMax: "8g", ApprovalSecs: 120, NoEgressProxy: true, Env: []string{"GOFLAGS"},
		Servers: map[string]json.RawMessage{"docs": json.RawMessage(`{"type":"http","url":"https://docs.example.com/mcp"}`),
			"linear": json.RawMessage(`{"command":"npx","args":["-y","linear-mcp"]}`)},
		Egress: []sandbox.EgressRule{{Host: "proxy.golang.org", Allow: true}, {Host: "ads.example.com"}},
		Projects: map[string]sandbox.ProjectConfig{
			lw.Repo: {Egress: []sandbox.EgressRule{{Host: "registry.npmjs.org", Port: 443, Allow: true}}},
			other:   {Rules: []sandbox.PolicyRule{{Tool: "oriel__push", Action: "deny"}}, Image: "billing:dev"},
		}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("the host has\n%+v\nwant\n%+v", cfg, want)
	}
	if err := s.Registry().Options().Set("sandbox.pids_limit", registry.LayerUser, registry.At{}, 256); err != nil {
		t.Fatal(err)
	}
	waitSandbox(t, lw, "the change", func(c sandbox.Config) bool { return c.PidsLimit == 256 })
}

func waitSandbox(t *testing.T, lw *rig.Host, what string, ok func(sandbox.Config) bool) sandbox.Config {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c := lw.W.SandboxConfig()
		if ok(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; the host has %+v", what, c)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// The page's sandbox section edits each list option in a list editor (a row per
// item, ✕ to remove it, a field to add one, each item checked as it's typed) or, where the option offers choices, a
// multi-select; and sandbox.servers in a dialog.
func TestSettingsSandboxSection(t *testing.T) {
	claude := agenttest.NewFake()
	claude.Name = "claude"
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir(), Config: apptest.Builtin(claude)})
	path := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, path, "[sandbox]\nmemory = \"4g\"\negress = [\"allow proxy.golang.org\", \"allow *.github.com:443\"]\n"+
		"servers = { docs = \"https://docs.example.com/mcp\", linear = \"npx -y linear-mcp\" }\n")
	settingsOf(s).Reload()
	s.Run("Settings", registry.Args{"args": "sandbox"})
	s.Golden("settings-sandbox")
	text := s.Text()
	for _, want := range []string{"sandbox.egress", "sandbox.go_cache_max", "sandbox.egress_proxy", "sandbox.default", "sandbox.backend", "sandbox.loopback",
		"none ▾"} {
		if !strings.Contains(text, want) {
			t.Errorf("the sandbox section lacks %q", want)
		}
	}
	if n := s.Node("sandbox.egress 2"); n.Value != "allow *.github.com:443" {
		t.Errorf("the egress list's second row = %q", n.Value)
	}
	get := func(name string) []string {
		v, _, _ := s.Registry().Options().Get(name, registry.At{})
		l, _ := v.([]string)
		return l
	}

	search := func(q string) {
		s.Run("settings.open", registry.Args{"query": q})
		s.Frame()
		s.Frame()
	}
	search("sandbox.memory")
	if text := s.Text(); !strings.Contains(text, "sandbox.memory") || !strings.Contains(text, "4g") {
		t.Error("the sandbox section lacks sandbox.memory at 4g")
	}
	s.Type("Esc")
	s.Frame()
	search("sandbox.rules")
	if !strings.Contains(s.Text(), "sandbox.rules") {
		t.Error("the sandbox section lacks sandbox.rules")
	}
	s.Type("Esc")
	s.Frame()
	search("sandbox.servers")
	if !strings.Contains(s.Text(), "2 servers") {
		t.Error("sandbox.servers' row doesn't sum up its servers")
	}
	s.Type("Esc")
	s.Frame()

	search("sandbox.rules")
	s.TypeInto("Add to sandbox.rules", "deny oriel__push")
	s.Type("CR")
	s.Frame()
	s.TypeInto("Add to sandbox.rules", "ask github__*")
	s.Type("CR")
	s.Frame()
	if got := get("sandbox.rules"); !slices.Equal(got, []string{"deny oriel__push", "ask github__*"}) {
		t.Errorf("sandbox.rules = %v", got)
	}
	if src := rig.ReadFile(t, path); !strings.Contains(src, `rules = ["deny oriel__push", "ask github__*"]`) {
		t.Errorf("the file:\n%s", src)
	}
	s.TypeInto("Add to sandbox.rules", "always oriel__push")
	s.Frame()
	if !strings.Contains(s.Text(), "a gateway rule starts allow, ask or deny") {
		t.Error("an invalid rule doesn't say why on its row")
	}
	s.Type("CR")
	if got := get("sandbox.rules"); len(got) != 2 {
		t.Errorf("an invalid rule was added: %v", got)
	}
	s.Type("Esc")
	s.Frame()

	search("sandbox.egress")
	s.Click(apptest.Centre(s.Node("Remove allow proxy.golang.org").Bounds), 0)
	if got := get("sandbox.egress"); !slices.Equal(got, []string{"allow *.github.com:443"}) {
		t.Errorf("✕ on the first egress rule: %v", got)
	}

	t.Setenv("ORIEL_PICK_ME", "1")
	search("sandbox.env")
	s.ClickText("none ▾")
	for _, r := range "ORIEL_PICK" {
		s.Type(string(r))
	}
	s.Click(apptest.Centre(s.Node("ORIEL_PICK_ME").Bounds), 0)
	if got := get("sandbox.env"); !slices.Equal(got, []string{"ORIEL_PICK_ME"}) {
		t.Errorf("picking a variable for sandbox.env: %v", got)
	}
}

// sandbox.servers' row sums up its servers and edits them in a dialog: Escape
// discards, Save writes the table.
func TestSettingsServersDialog(t *testing.T) {
	s := apptest.New(t, apptest.Options{WorkDir: t.TempDir()})
	path := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, path, "[sandbox]\nservers = { docs = \"https://docs.example.com/mcp\", linear = \"npx -y linear-mcp\" }\n")
	settingsOf(s).Reload()
	s.Run("settings.open", registry.Args{"query": "sandbox.servers"})
	s.Frame()
	s.Frame()
	s.ClickText("Edit…")
	s.Frame()
	if n := s.Node("server 2"); n.Value != "npx -y linear-mcp" {
		t.Fatalf("the dialog's second server = %q", n.Value)
	}
	s.TypeInto("New name", "Git Hub")
	s.TypeInto("New server", "https://api.example.com/mcp")
	s.Frame()
	if !strings.Contains(s.Text(), "a server's name is lower case") {
		t.Errorf("an invalid name doesn't say why:\n%s", s.Text())
	}
	s.Golden("settings-servers")
	s.Type("Esc")
	s.Frame()
	if !s.Shown("settings") || strings.Contains(s.Text(), "New name") {
		t.Fatal("Escape didn't close just the dialog")
	}
	if src := rig.ReadFile(t, path); strings.Contains(src, "api.example.com") {
		t.Errorf("Escape wrote:\n%s", src)
	}

	s.ClickText("Edit…")
	s.Frame()
	s.Click(apptest.Centre(s.Node("Remove docs").Bounds), 0)
	s.Frame()
	s.TypeInto("New name", "github")
	s.TypeInto("New server", "https://api.example.com/mcp")
	s.Type("CR")
	s.Frame()
	s.ClickText("Save")
	s.Frame()
	want := map[string]string{"github": "https://api.example.com/mcp", "linear": "npx -y linear-mcp"}
	if v, _, _ := s.Registry().Options().Get("sandbox.servers", registry.At{}); !maps.Equal(v.(map[string]string), want) {
		t.Errorf("sandbox.servers = %v", v)
	}
	if src := rig.ReadFile(t, path); !strings.Contains(src, `servers = { github = "https://api.example.com/mcp", linear = "npx -y linear-mcp" }`) {
		t.Errorf("the file:\n%s", src)
	}
	if !strings.Contains(s.Text(), "2 servers") {
		t.Error("the row doesn't sum up the servers")
	}
}
