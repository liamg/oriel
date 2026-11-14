package settings

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/app/kit"
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
func (x *Catalog) readBundled() {
	x.bundled = map[string]*bundledPlugin{}
	for _, b := range x.Bundled {
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
func (x *Catalog) self() []string {
	if s := x.Self; len(s) > 0 {
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
func (x *Catalog) syncBundled(installed func(id string) bool) {
	off, _ := registry.Option[[]string](x.h.Options(), BundledDisabledOption, registry.At{})
	for _, id := range x.bundledIDs {
		bp := x.bundled[id]
		want := "loaded"
		switch {
		case slices.Contains(off, id):
			want = "disabled"
		case installed(id):
			want = "replaced"
		}
		if want != "loaded" {
			if bp.stop != nil {
				bp.stop.Dispose()
				bp.stop = nil
			}
			bp.state = want
			continue
		}
		switch {
		case bp.stop != nil: // running, or waiting for its first use
		case !bp.m.Lazy.Empty():
			bp.stop = x.h.Placeholder(bp.m.Lazy.Placeholder(x.h, id, id, func() error { return x.startBundled(bp) }))
			bp.state = waitsState
		default:
			x.startBundled(bp)
		}
	}
	x.h.Redraw()
}

// waitsState is a lazy plugin's state before its first use.
const waitsState = "waits for first use"

// startBundled starts bp's Lua, in place of its placeholder if it waited behind one.
func (x *Catalog) startBundled(bp *bundledPlugin) error {
	id := bp.m.ID
	stop, err := x.loadScript(bp.b.FS, kit.ScriptOpts{Data: filepath.Join(x.StateDir, "plugins", id),
		Programs: plugin.BundledPrograms(bp.b, x.self())})
	if err != nil {
		x.h.Notify(core.Notice{ID: "plugins.failed." + id, Severity: core.SevError, Title: "Plugin " + id, Text: err.Error()})
		bp.stop, bp.state = nil, "failed"
		return err
	}
	bp.stop, bp.state = stop, "loaded"
	x.h.Redraw()
	return nil
}

// loadScript starts the Lua plugin at fsys's root, as the window makes one.
func (x *Catalog) loadScript(fsys fs.FS, o kit.ScriptOpts) (registry.Disposable, error) {
	s, ok := kit.ScriptsOf(x.h)
	if !ok {
		return nil, errors.New("this window doesn't run Lua plugins")
	}
	return x.h.Load(s.Script(fsys, o))
}

// stopBundled stops every bundled plugin.
func (x *Catalog) stopBundled() {
	for _, bp := range x.bundled {
		if bp.stop != nil {
			bp.stop.Dispose()
			bp.stop = nil
		}
	}
}
