package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// BundledDisabledOption lists the bundled plugins turned off, by id.
const BundledDisabledOption = "bundled.disabled"

// bundledPlugin is a plugin shipped in the binary, as the plugin manager runs it.
type bundledPlugin struct {
	b     plugin.Bundled
	m     plugin.Manifest
	stop  registry.Disposable
	state string // loaded, disabled, replaced (an install of its id runs instead), failed
}

// readBundled reads each bundled plugin's manifest; one that can't be read says so and is left out.
func (x *catalogPlugin) readBundled() {
	x.bundled = map[string]*bundledPlugin{}
	for _, b := range x.sh.cfg.Bundled {
		raw, err := fs.ReadFile(b.FS, plugin.ManifestFile)
		var m plugin.Manifest
		if err == nil {
			m, err = plugin.ParseManifest(raw)
		}
		if err != nil {
			x.h.Notify(core.Notice{ID: "plugins.bundled", Severity: core.SevError, Title: "Bundled plugin", Text: err.Error()})
			continue
		}
		x.bundled[m.ID] = &bundledPlugin{b: b, m: m}
		x.bundledIDs = append(x.bundledIDs, m.ID)
	}
}

// self is the command that runs oriel itself, for bundled plugins' processes.
func (x *catalogPlugin) self() []string {
	if s := x.sh.cfg.Self; len(s) > 0 {
		return s
	}
	exe, err := os.Executable()
	if err != nil {
		return []string{"oriel"}
	}
	return []string{exe}
}

// syncBundled runs each bundled plugin that's neither turned off nor installed, and stops the rest. It runs on the
// loop.
func (x *catalogPlugin) syncBundled(installed func(id string) bool) {
	v, _, _ := x.h.Options().Get(BundledDisabledOption, registry.At{})
	off, _ := v.([]string)
	for _, id := range x.bundledIDs {
		bp := x.bundled[id]
		want := "loaded"
		switch {
		case slices.Contains(off, id):
			want = "disabled"
		case installed(id):
			want = "replaced"
		}
		if want != "loaded" && bp.stop != nil {
			bp.stop.Dispose()
			bp.stop = nil
		}
		if want == "loaded" && bp.stop == nil {
			p := ScriptPluginWith(bp.b.FS, PluginOpts{Data: filepath.Join(x.sh.cfg.StateDir, "plugins", id),
				Programs: plugin.BundledPrograms(bp.b, x.self())})
			stop, err := x.h.Load(p)
			if err != nil {
				x.h.Notify(core.Notice{ID: "plugins.failed." + id, Severity: core.SevError, Title: "Plugin " + id, Text: err.Error()})
				want = "failed"
			}
			bp.stop = stop
		}
		bp.state = want
	}
	x.h.Redraw()
}

// stopBundled stops every bundled plugin.
func (x *catalogPlugin) stopBundled() {
	for _, bp := range x.bundled {
		if bp.stop != nil {
			bp.stop.Dispose()
			bp.stop = nil
		}
	}
}
