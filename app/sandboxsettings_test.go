package app

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

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/settingsfile"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/worker"
)

// settings.md, "sandbox.*": the sandbox's settings are options of the right types, with the project layer only
// where a repo may set its own (its network and gateway rules, its image).
func TestSandboxOptionsAreDeclared(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	o := s.reg.Options()
	for _, tt := range []struct {
		name    string
		typ     registry.OptionType
		def     any
		project bool
	}{
		{"sandbox.default", registry.Enum, "sandbox", false},
		{"sandbox.image", registry.String, "", true},
		{"sandbox.claude_version", registry.String, "", false},
		{"sandbox.env", registry.List, []string{}, false},
		{"sandbox.egress", registry.List, []string{}, true},
		{"sandbox.rules", registry.List, []string{}, true},
		{"sandbox.approval_timeout", registry.Duration, time.Duration(0), false},
		{"sandbox.auto_allow_read_only", registry.Bool, false, false},
		{"sandbox.hidden_servers", registry.List, []string{}, false},
		{"sandbox.egress_proxy", registry.Bool, true, false},
		{"sandbox.memory", registry.String, "", false},
		{"sandbox.pids_limit", registry.Int, 0, false},
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
		"sandbox.memory": "lots", "sandbox.approval_timeout": -time.Second, "sandbox.servers": map[string]string{"Git Hub": "https://x"},
		"sandbox.servers ": map[string]string{"gh": `npx "open`}} {
		name = strings.TrimSpace(name)
		if err := o.Set(name, registry.LayerUser, registry.At{}, bad); err == nil {
			t.Errorf("%s took %v", name, bad)
		}
	}
}

// agents.md, "Sandbox settings": your sandbox.* values, and each trusted project file's own, reach the worker as the
// sandbox.Config its sandboxes run on; a repo a session works in brings its project file even when the window is
// elsewhere, and a change reaches the worker again.
func TestSandboxSettingsReachTheWorker(t *testing.T) {
	lw := newLocalWorker(t)
	other := filepath.Join(t.TempDir(), "billing")
	gitRepo(t, other, map[string]string{"README.md": "billing\n"}, 1)
	s := newSceneWith(t, "", lw.repo, mockupSize, lw.connect(t))
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), `[sandbox]
memory = "4g"
egress = ["allow proxy.golang.org", "deny ads.example.com"]
approval_timeout = "2m"
egress_proxy = false
env = ["GOFLAGS"]

[sandbox.servers]
docs = "https://docs.example.com/mcp"
linear = "npx -y linear-mcp"
`)
	writeFile(t, filepath.Join(lw.repo, ".oriel", "settings.toml"), "[sandbox]\negress = [\"allow registry.npmjs.org:443\"]\n")
	writeFile(t, filepath.Join(other, ".oriel", "settings.toml"), "[sandbox]\nrules = [\"deny oriel__push\"]\nimage = \"billing:dev\"\n")
	set := s.settings()
	set.loadAll()
	if err := set.trustFile(filepath.Join(lw.repo, ".oriel", "settings.toml")); err != nil {
		t.Fatal(err)
	}
	if _, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Repos: []core.RepoID{core.RepoID(other)}}); err != nil {
		t.Fatal(err)
	}
	waitState(t, s.store, "the session on billing", func(st core.State) bool { return len(st.Sessions) == 1 })
	set.loadAll()
	if err := set.trustFile(filepath.Join(other, ".oriel", "settings.toml")); err != nil {
		t.Fatal(err)
	}
	cfg := waitSandbox(t, lw, "the user's and both projects' settings", func(c sandbox.Config) bool {
		return len(c.Projects[other].Rules) == 1 && len(c.Projects[lw.repo].Egress) == 1
	})
	want := sandbox.Config{Default: "sandbox", Backend: "docker", Loopback: "all", Memory: "4g", ApprovalSecs: 120, NoEgressProxy: true, Env: []string{"GOFLAGS"},
		Servers: map[string]json.RawMessage{"docs": json.RawMessage(`{"type":"http","url":"https://docs.example.com/mcp"}`),
			"linear": json.RawMessage(`{"command":"npx","args":["-y","linear-mcp"]}`)},
		Egress: []sandbox.EgressRule{{Host: "proxy.golang.org", Allow: true}, {Host: "ads.example.com"}},
		Projects: map[string]sandbox.ProjectConfig{
			lw.repo: {Egress: []sandbox.EgressRule{{Host: "registry.npmjs.org", Port: 443, Allow: true}}},
			other:   {Rules: []sandbox.PolicyRule{{Tool: "oriel__push", Action: "deny"}}, Image: "billing:dev"},
		}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("the worker has\n%+v\nwant\n%+v", cfg, want)
	}
	if err := s.reg.Options().Set("sandbox.pids_limit", registry.LayerUser, registry.At{}, 256); err != nil {
		t.Fatal(err)
	}
	waitSandbox(t, lw, "the change", func(c sandbox.Config) bool { return c.PidsLimit == 256 })
}

func waitSandbox(t *testing.T, lw *localWorker, what string, ok func(sandbox.Config) bool) sandbox.Config {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		c := lw.w.SandboxConfig()
		if ok(c) {
			return c
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s; the worker has %+v", what, c)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// settings.md, "Sandbox settings": the page's sandbox section edits each list option in a list editor (a row per
// item, ✕ to remove it, a field to add one, each item checked as it's typed) or, where the option offers choices, a
// multi-select; and sandbox.servers in a dialog.
func TestSettingsSandboxSection(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, path, "[sandbox]\nmemory = \"4g\"\negress = [\"allow proxy.golang.org\", \"allow *.github.com:443\"]\n"+
		"servers = { docs = \"https://docs.example.com/mcp\", linear = \"npx -y linear-mcp\" }\n")
	s.settings().loadAll()
	s.run("Settings", registry.Args{"args": "sandbox"})
	s.golden("settings-sandbox")
	text := s.frameText()
	for _, want := range []string{"sandbox.egress", "sandbox.memory", "4g", "sandbox.egress_proxy", "sandbox.default", "sandbox.backend", "sandbox.loopback",
		"none ▾"} {
		if !strings.Contains(text, want) {
			t.Errorf("the sandbox section lacks %q", want)
		}
	}
	if n := s.access("sandbox.egress 2"); n.Value != "allow *.github.com:443" {
		t.Errorf("the egress list's second row = %q", n.Value)
	}
	get := func(name string) []string {
		v, _, _ := s.reg.Options().Get(name, registry.At{})
		l, _ := v.([]string)
		return l
	}

	search := func(q string) {
		s.run("settings.open", registry.Args{"query": q})
		s.frame()
		s.frame()
	}
	search("sandbox.rules")
	if !strings.Contains(s.frameText(), "sandbox.rules") {
		t.Error("the sandbox section lacks sandbox.rules")
	}
	s.typeKeys("Esc")
	s.frame()
	search("sandbox.servers")
	if !strings.Contains(s.frameText(), "2 servers") {
		t.Error("sandbox.servers' row doesn't sum up its servers")
	}
	s.typeKeys("Esc")
	s.frame()

	search("sandbox.rules")
	s.typeInto(t, "Add to sandbox.rules", "deny oriel__push")
	s.typeKeys("CR")
	s.frame()
	s.typeInto(t, "Add to sandbox.rules", "ask github__*")
	s.typeKeys("CR")
	s.frame()
	if got := get("sandbox.rules"); !slices.Equal(got, []string{"deny oriel__push", "ask github__*"}) {
		t.Errorf("sandbox.rules = %v", got)
	}
	if src := readFile(t, path); !strings.Contains(src, `rules = ["deny oriel__push", "ask github__*"]`) {
		t.Errorf("the file:\n%s", src)
	}
	s.typeInto(t, "Add to sandbox.rules", "always oriel__push")
	s.frame()
	if !strings.Contains(s.frameText(), "a gateway rule starts allow, ask or deny") {
		t.Error("an invalid rule doesn't say why on its row")
	}
	s.typeKeys("CR")
	if got := get("sandbox.rules"); len(got) != 2 {
		t.Errorf("an invalid rule was added: %v", got)
	}
	s.typeKeys("Esc")
	s.frame()

	search("sandbox.egress")
	s.click(centre(s.access("Remove allow proxy.golang.org").Bounds))
	if got := get("sandbox.egress"); !slices.Equal(got, []string{"allow *.github.com:443"}) {
		t.Errorf("✕ on the first egress rule: %v", got)
	}

	t.Setenv("ORIEL_PICK_ME", "1")
	search("sandbox.env")
	s.clickText(t, "none ▾")
	for _, r := range "ORIEL_PICK" {
		s.typeKeys(string(r))
	}
	s.click(centre(s.access("ORIEL_PICK_ME").Bounds))
	if got := get("sandbox.env"); !slices.Equal(got, []string{"ORIEL_PICK_ME"}) {
		t.Errorf("picking a variable for sandbox.env: %v", got)
	}
}

// settings.md, "Sandbox settings": sandbox.servers' row sums up its servers and edits them in a dialog: Escape
// discards, Save writes the table.
func TestSettingsServersDialog(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	path := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, path, "[sandbox]\nservers = { docs = \"https://docs.example.com/mcp\", linear = \"npx -y linear-mcp\" }\n")
	s.settings().loadAll()
	s.run("settings.open", registry.Args{"query": "sandbox.servers"})
	s.frame()
	s.frame()
	s.clickText(t, "Edit…")
	s.frame()
	if n := s.access("server 2"); n.Value != "npx -y linear-mcp" {
		t.Fatalf("the dialog's second server = %q", n.Value)
	}
	s.typeInto(t, "New name", "Git Hub")
	s.typeInto(t, "New server", "https://api.example.com/mcp")
	s.frame()
	if !strings.Contains(s.frameText(), "a server's name is lower case") {
		t.Errorf("an invalid name doesn't say why:\n%s", s.frameText())
	}
	s.golden("settings-servers")
	s.typeKeys("Esc")
	s.frame()
	if !s.settings().open || strings.Contains(s.frameText(), "New name") {
		t.Fatal("Escape didn't close just the dialog")
	}
	if src := readFile(t, path); strings.Contains(src, "api.example.com") {
		t.Errorf("Escape wrote:\n%s", src)
	}

	s.clickText(t, "Edit…")
	s.frame()
	s.click(centre(s.access("Remove docs").Bounds))
	s.frame()
	s.typeInto(t, "New name", "github")
	s.typeInto(t, "New server", "https://api.example.com/mcp")
	s.typeKeys("CR")
	s.frame()
	s.clickText(t, "Save")
	s.frame()
	want := map[string]string{"github": "https://api.example.com/mcp", "linear": "npx -y linear-mcp"}
	if v, _, _ := s.reg.Options().Get("sandbox.servers", registry.At{}); !maps.Equal(v.(map[string]string), want) {
		t.Errorf("sandbox.servers = %v", v)
	}
	if src := readFile(t, path); !strings.Contains(src, `servers = { github = "https://api.example.com/mcp", linear = "npx -y linear-mcp" }`) {
		t.Errorf("the file:\n%s", src)
	}
	if !strings.Contains(s.frameText(), "2 servers") {
		t.Error("the row doesn't sum up the servers")
	}
}

// settings.md, "Options": a map is a TOML table (inline or its own), a Lua table, and a value at any layer.
func TestMapOptionLayers(t *testing.T) {
	work := t.TempDir()
	s := luaScene(t, "oriel.option[\"test.hosts\"] = { db = \"10.0.0.2\", [\"cache-1\"] = \"10.0.0.3\" }\n")
	s.reg.Option(registry.OptionSpec{Name: "test.hosts", Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser | registry.ScopeProject,
		KeyLabel: "name", ValueLabel: "address"})
	s.lua().reload(false)
	o := s.reg.Options()
	if v, ok := o.Lookup("test.hosts", registry.LayerLua, registry.At{}); !ok || !maps.Equal(v.(map[string]string), map[string]string{"db": "10.0.0.2", "cache-1": "10.0.0.3"}) {
		t.Errorf("from Lua: %v", v)
	}
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), "[test.hosts]\nweb = \"10.0.0.1\"\n")
	project := filepath.Join(work, ".oriel", "settings.toml")
	writeFile(t, project, "[test]\nhosts = { db = \"192.168.0.9\" }\n")
	s.settings().project = projectFile(work)
	s.settings().loadAll()
	s.settings().trustFile(project)
	if v, ok := o.Lookup("test.hosts", registry.LayerUser, registry.At{}); !ok || v.(map[string]string)["web"] != "10.0.0.1" {
		t.Errorf("from the user file's [test.hosts]: %v", v)
	}
	if v, l, _ := o.Get("test.hosts", registry.At{Project: work}); l != registry.LayerProject || v.(map[string]string)["db"] != "192.168.0.9" {
		t.Errorf("at the project: %v from %v", v, l)
	}
	if got := show(s.eval(t, `oriel.option["test.hosts"].db`)[0]); got != `"10.0.0.2"` {
		t.Errorf("reading it in Lua: %s", got)
	}
	o.Unset("test.hosts", registry.LayerLua, registry.At{})
	if err := s.reg.Run(context.Background(), "settings.set", registry.Args{"name": "test.hosts", "value": map[string]any{"web": "10.0.0.7"}}); err != nil {
		t.Fatal(err)
	}
	if src := readFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")); !strings.Contains(src, `hosts = { web = "10.0.0.7" }`) {
		t.Errorf("writing over a [table] by hand:\n%s", src)
	}
	if got := luaValue(map[string]string{"cache-1": "a", "db": "b"}); got != `{ ["cache-1"] = "a", db = "b" }` {
		t.Errorf("as Lua: %s", got)
	}
	if got := formatValue(map[string]string{"cache 1": "a", "db": "b"}); got != `{ "cache 1" = "a", db = "b" }` {
		t.Errorf("as TOML: %s", got)
	}
}

// agents.md, "Sandbox settings": a worker elsewhere gets your values and reads its own repos' project files; the
// app's are of paths on this machine.
func TestRemoteWorkersReadTheirOwnProjects(t *testing.T) {
	cfg := sandbox.Config{Memory: "4g", Projects: map[string]sandbox.ProjectConfig{"/src/pricing": {Image: "x"}}}
	if got := forWorker(cfg, core.LocalWorker); !reflect.DeepEqual(got, cfg) {
		t.Errorf("this machine's worker gets %+v", got)
	}
	if got := forWorker(cfg, "devbox"); got.Projects != nil || !got.ReadProjects || got.Memory != "4g" {
		t.Errorf("a worker elsewhere gets %+v", got)
	}
}

// A worker elsewhere is told to read its own project files, and a notice of its offers Trust, which trusts the file
// on that worker's machine.
func TestTrustOnAWorkerElsewhere(t *testing.T) {
	trusted := filepath.Join(t.TempDir(), "trusted.toml")
	devbox := newWorkerWith(t, worker.Options{ID: "devbox", Name: "devbox", TrustPath: trusted})
	file := filepath.Join(devbox.repo, ".oriel", "settings.toml")
	writeFile(t, file, "[sandbox]\nimage = \"billing:dev\"\n")
	ctx := context.Background()
	if _, err := devbox.client(t).AddRepoPath(ctx, devbox.repo); err != nil {
		t.Fatal(err)
	}
	s := newSceneWith(t, "", t.TempDir(), mockupSize, devbox.connect(t))
	waitSandbox(t, devbox, "the settings, to read its projects with", func(c sandbox.Config) bool { return c.ReadProjects && c.Default == "sandbox" })
	if err := s.reg.Run(ctx, "settings.trust", registry.Args{"path": file, "worker": "devbox"}); err != nil {
		t.Fatal(err)
	}
	if !(settingsfile.Trust{Path: trusted}).Trusted(file, []byte(readFile(t, file))) {
		t.Error("the file isn't trusted on the worker")
	}
}

// sandbox.default is where the launcher's Sandbox toggle starts, a sandbox unless it says host; a command that
// names sandbox still wins.
func TestLauncherSandboxStartsFromTheOption(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	var l *launcher
	for _, e := range s.plugins {
		if x, ok := e.(*launcher); ok {
			l = x
		}
	}
	l.openWith(registry.Args{})
	if !l.sandbox {
		t.Error("the sandbox is off by default")
	}
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	l.openWith(registry.Args{})
	if l.sandbox {
		t.Error("the sandbox is on with sandbox.default = host")
	}
	l.openWith(registry.Args{"sandbox": true})
	if !l.sandbox {
		t.Error("session.new {sandbox = true} has the sandbox off")
	}
}
