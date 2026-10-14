package app

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
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
		{"sandbox.default", registry.Enum, "host", false},
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
	for name, bad := range map[string]any{"sandbox.egress": []string{"allow"}, "sandbox.rules": []string{"always oriel__push"},
		"sandbox.memory": "lots", "sandbox.approval_timeout": -time.Second} {
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
	want := sandbox.Config{Default: "host", Memory: "4g", ApprovalSecs: 120, NoEgressProxy: true, Env: []string{"GOFLAGS"},
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

// The settings page has a sandbox section, a list shown as its items and set from them.
func TestSettingsSandboxSection(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), "[sandbox]\nmemory = \"4g\"\negress = [\"allow proxy.golang.org\", \"allow *.github.com:443\"]\n")
	s.settings().loadAll()
	s.run("Settings", registry.Args{"args": "sandbox"})
	s.golden("settings-sandbox")
	text := s.frameText()
	for _, want := range []string{"sandbox.egress", "allow proxy.golang.org, allow *.github.com:443", "sandbox.memory", "4g", "sandbox.egress_proxy",
		"sandbox.rules", "sandbox.default"} {
		if !strings.Contains(text, want) {
			t.Errorf("the sandbox section lacks %q", want)
		}
	}
	s.typeInto(t, "sandbox.rules", "deny oriel__push, ask github__*")
	s.typeKeys("CR")
	v, _, _ := s.reg.Options().Get("sandbox.rules", registry.At{})
	if got, _ := v.([]string); !slices.Equal(got, []string{"deny oriel__push", "ask github__*"}) {
		t.Errorf("sandbox.rules = %v", v)
	}
	if src := readFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")); !strings.Contains(src, `rules = ["deny oriel__push", "ask github__*"]`) {
		t.Errorf("the file:\n%s", src)
	}
}

// sandbox.default is where the launcher's Sandbox toggle starts; a command that names sandbox still wins.
func TestLauncherSandboxStartsFromTheOption(t *testing.T) {
	s := newScene(t, "", t.TempDir(), mockupSize)
	var l *launcher
	for _, e := range s.exts {
		if x, ok := e.(*launcher); ok {
			l = x
		}
	}
	l.openWith(registry.Args{})
	if l.sandbox {
		t.Error("the sandbox is on with sandbox.default = host")
	}
	if err := s.reg.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "sandbox"); err != nil {
		t.Fatal(err)
	}
	l.openWith(registry.Args{})
	if !l.sandbox {
		t.Error("the sandbox is off with sandbox.default = sandbox")
	}
	l.openWith(registry.Args{"sandbox": false})
	if l.sandbox {
		t.Error("session.new {sandbox = false} has the sandbox on")
	}
}
