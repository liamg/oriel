package app_test

import (
	"context"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/registry"
)

// A map option is a TOML table (inline or its own), a Lua table, and a value at any layer.
func TestMapOptionLayers(t *testing.T) {
	work := t.TempDir()
	s := luaSceneAt(t, work, "oriel.option[\"test.hosts\"] = { db = \"10.0.0.2\", [\"cache-1\"] = \"10.0.0.3\" }\n")
	s.Registry().Option(registry.OptionSpec{Name: "test.hosts", Type: registry.Map, Default: map[string]string{}, Scopes: registry.ScopeUser | registry.ScopeProject,
		KeyLabel: "name", ValueLabel: "address"})
	s.ReloadLua()
	o := s.Registry().Options()
	if v, ok := o.Lookup("test.hosts", registry.LayerLua, registry.At{}); !ok || !maps.Equal(v.(map[string]string), map[string]string{"db": "10.0.0.2", "cache-1": "10.0.0.3"}) {
		t.Errorf("from Lua: %v", v)
	}
	rig.WriteFile(t, filepath.Join(s.ConfigDir, "settings.toml"), "[test.hosts]\nweb = \"10.0.0.1\"\n")
	project := filepath.Join(work, ".oriel", "settings.toml")
	rig.WriteFile(t, project, "[test]\nhosts = { db = \"192.168.0.9\" }\n")
	settingsPlugin(s).Reload()
	s.Run("settings.trust", registry.Args{"path": project})
	if v, ok := o.Lookup("test.hosts", registry.LayerUser, registry.At{}); !ok || v.(map[string]string)["web"] != "10.0.0.1" {
		t.Errorf("from the user file's [test.hosts]: %v", v)
	}
	if v, l, _ := o.Get("test.hosts", registry.At{Project: work}); l != registry.LayerProject || v.(map[string]string)["db"] != "192.168.0.9" {
		t.Errorf("at the project: %v from %v", v, l)
	}
	// The window is on work, so Lua reads the project's value there.
	if got := settings.Show(s.Lua(`oriel.option["test.hosts"].db`)[0]); got != `"192.168.0.9"` {
		t.Errorf("reading it in Lua: %s", got)
	}
	o.Unset("test.hosts", registry.LayerLua, registry.At{})
	if err := s.Registry().Run(context.Background(), "settings.set", registry.Args{"name": "test.hosts", "value": map[string]any{"web": "10.0.0.7"}}); err != nil {
		t.Fatal(err)
	}
	if src := rig.ReadFile(t, filepath.Join(s.ConfigDir, "settings.toml")); !strings.Contains(src, `hosts = { web = "10.0.0.7" }`) {
		t.Errorf("writing over a [table] by hand:\n%s", src)
	}
}
