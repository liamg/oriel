package app_test

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/plugins"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// Options are named by the part of a plugin's id after the slash, so a second plugin of that name could change the
// first's: it isn't loaded, and a notice names the plugin that has the name.
func TestPluginWithATakenNameIsNotLoaded(t *testing.T) {
	hi := plugin.Bundled{FS: fstest.MapFS{
		"oriel.toml": {Data: []byte("id = \"acme/hi\"\nversion = \"1.0.0\"\n")},
		"init.lua":   {Data: []byte(hiPlugin)},
	}}
	dir := writePlugin(t, map[string]string{"oriel.toml": "id = \"other/hi\"\nversion = \"1.0.0\"\n", "init.lua": `oriel.notify("other loaded")`})
	s, settings := catalogScene(t, "", func(cfg *apptest.Config) { cfg.Bundled = []plugin.Bundled{hi} })
	rig.WriteFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n\"other/hi\" = { path = \""+dir+"\" }\n")
	syncPlugins(s, t)
	loaded := func(id string) bool {
		return slices.ContainsFunc(s.Host().Plugins(), func(m plugin.Manifest) bool { return m.ID == id })
	}
	if loaded("other/hi") || noticed(s, "other loaded") > 0 {
		t.Error("other/hi loaded beside acme/hi")
	}
	if !loaded("acme/hi") {
		t.Error("acme/hi isn't loaded")
	}
	found := false
	for _, n := range s.Store().State().Notifications {
		found = found || strings.Contains(n.Title+n.Text, "other/hi") && strings.Contains(n.Text, "acme/hi")
	}
	if !found {
		t.Errorf("no notice names acme/hi; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
}

// A bundled plugin keeps its name when it's turned back on: an installed plugin that took the name while it was
// off is stopped, and a notice says why.
func TestBundledPluginTurnedBackOnTakesItsName(t *testing.T) {
	hi := plugin.Bundled{FS: fstest.MapFS{
		"oriel.toml": {Data: []byte("id = \"acme/hi\"\nversion = \"1.0.0\"\n")},
		"init.lua":   {Data: []byte(hiPlugin)},
	}}
	dir := writePlugin(t, map[string]string{"oriel.toml": "id = \"other/hi\"\nversion = \"1.0.0\"\n", "init.lua": `oriel.notify("other loaded")`})
	s, settings := catalogScene(t, "", func(cfg *apptest.Config) { cfg.Bundled = []plugin.Bundled{hi} })
	x := apptest.PluginOf[*plugins.Catalog](s, "catalog")
	s.Run("settings.set", registry.Args{"name": plugins.BundledDisabledOption, "value": []string{"acme/hi"}, "scope": "user"})
	s.Until("acme/hi off", func() bool { return x.State("acme/hi") == "disabled" })
	off, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	rig.WriteFile(t, settings, string(off)+"\n[plugins]\n\"other/hi\" = { path = \""+dir+"\" }\n")
	syncPlugins(s, t)
	loaded := func(id string) bool {
		return slices.ContainsFunc(s.Host().Plugins(), func(m plugin.Manifest) bool { return m.ID == id })
	}
	if !loaded("other/hi") {
		t.Fatal("other/hi didn't load while acme/hi was off")
	}
	s.Run("settings.set", registry.Args{"name": plugins.BundledDisabledOption, "value": []string{}, "scope": "user"})
	s.Until("acme/hi on", func() bool { return x.State("acme/hi") == "loaded" })
	if loaded("other/hi") {
		t.Error("other/hi still runs beside acme/hi")
	}
	found := false
	for _, n := range s.Store().State().Notifications {
		found = found || strings.Contains(n.Title+n.Text, "other/hi") && strings.Contains(n.Text, "acme/hi")
	}
	if !found {
		t.Errorf("no notice names both; notices: %v", noticeTexts(s.Store().State().Notifications))
	}
}

// Taking out or turning off the plugin that has a name lets the one refused for it load: the one going away stops
// before the others load.
func TestPluginRefusedForANameLoadsWhenTheOtherGoes(t *testing.T) {
	for _, tc := range []struct{ name, zed string }{
		{"uninstalled", ""},
		{"disabled", "\"zed/tools\" = { path = \"%s\", enabled = false }\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			zed := writePlugin(t, map[string]string{"oriel.toml": "id = \"zed/tools\"\nversion = \"1.0.0\"\n", "init.lua": `oriel.notify("zed loaded")`})
			bob := writePlugin(t, map[string]string{"oriel.toml": "id = \"bob/tools\"\nversion = \"1.0.0\"\n", "init.lua": `oriel.notify("bob loaded")`})
			s, settings := catalogScene(t, "")
			head := "[ui]\ntheme = \"oriel\"\n\n[plugins]\n"
			rig.WriteFile(t, settings, head+"\"zed/tools\" = { path = \""+zed+"\" }\n")
			syncPlugins(s, t)
			rig.WriteFile(t, settings, head+"\"zed/tools\" = { path = \""+zed+"\" }\n\"bob/tools\" = { path = \""+bob+"\" }\n")
			syncPlugins(s, t)
			loaded := func(id string) bool {
				return slices.ContainsFunc(s.Host().Plugins(), func(m plugin.Manifest) bool { return m.ID == id })
			}
			if !loaded("zed/tools") || loaded("bob/tools") {
				t.Fatalf("before: zed %v, bob %v", loaded("zed/tools"), loaded("bob/tools"))
			}
			refused := func() int {
				n := 0
				for _, x := range s.Store().State().Notifications {
					if strings.Contains(x.Text, "bob/tools isn't loaded") {
						n += x.Count
					}
				}
				return n
			}
			before := refused()
			rest := ""
			if tc.zed != "" {
				rest = strings.Replace(tc.zed, "%s", zed, 1)
			}
			rig.WriteFile(t, settings, head+"\"bob/tools\" = { path = \""+bob+"\" }\n"+rest)
			syncPlugins(s, t)
			if loaded("zed/tools") || !loaded("bob/tools") {
				t.Errorf("after: zed %v, bob %v; notices: %v", loaded("zed/tools"), loaded("bob/tools"), noticeTexts(s.Store().State().Notifications))
			}
			if n := refused(); n != before {
				t.Errorf("bob/tools was refused again while zed/tools was going: %d refusals, %d before", n, before)
			}
		})
	}
}
