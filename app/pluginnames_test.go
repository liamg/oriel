package app_test

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/plugin"
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
