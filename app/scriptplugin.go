package app

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
)

// scriptPlugin is a plugin from its files (catalog.md, "What a plugin is"): its oriel.toml's [contributes], then its
// init.lua in a VM of its own whose every call is checked against the permissions it's granted (security.md). A
// data-only plugin has no init.lua.
type scriptPlugin struct {
	fsys    fs.FS
	o       PluginOpts
	m       plugin.Manifest
	err     error
	contrib registry.Disposable
	vm      *script.VM
	apis    *api.APIs
}

// PluginOpts is where a script plugin's own files are and what it's granted.
type PluginOpts struct {
	Dir    string         // its directory, where its processes and modules are (bin/…); none if ""
	Grants []plugin.Grant // what it may do; what its manifest declares if nil
	Data   string         // its data directory; plugins/<id> in the state directory if ""
}

// ScriptPlugin is the Lua plugin whose oriel.toml and init.lua are at fsys's root, granted what its manifest
// declares: one shipped with this build, whose consent is the build's. A plugin that fails to load says so in a
// notice and leaves the app running.
func ScriptPlugin(fsys fs.FS) plugin.Plugin { return ScriptPluginWith(fsys, PluginOpts{}) }

// ScriptPluginWith is ScriptPlugin with its directory, its grants and its data directory given.
func ScriptPluginWith(fsys fs.FS, o PluginOpts) plugin.Plugin {
	p := &scriptPlugin{fsys: fsys, o: o}
	b, err := fs.ReadFile(fsys, plugin.ManifestFile)
	if err == nil {
		p.m, err = plugin.ParseManifest(b)
	}
	if p.m.ID == "" {
		p.m.ID = "plugin"
	}
	p.err = err
	return p
}

// DirPlugin is the Lua plugin in dir, its processes started from there: ScriptPluginWith(os.DirFS(dir), o) with o.Dir
// set to dir.
func DirPlugin(dir string, o PluginOpts) plugin.Plugin {
	o.Dir = dir
	return ScriptPluginWith(os.DirFS(dir), o)
}

func (p *scriptPlugin) Manifest() plugin.Manifest { return p.m }

func (p *scriptPlugin) Activate(_ context.Context, h plugin.Host) error {
	if p.err != nil {
		p.failed(h, p.err)
		return nil
	}
	if !p.m.Contributes.Empty() {
		if p.o.Dir == "" {
			p.failed(h, errors.New("[contributes] needs the plugin's directory"))
			return nil
		}
		d, err := plugin.Contribute(h, p.o.Dir, p.m.ID, p.m.Contributes)
		if err != nil {
			p.failed(h, err)
			return nil
		}
		p.contrib = d
	}
	src, err := fs.ReadFile(p.fsys, plugin.EntryFile)
	if errors.Is(err, fs.ErrNotExist) && p.contrib != nil {
		return nil
	}
	if err != nil {
		p.Deactivate(context.Background())
		p.failed(h, err)
		return nil
	}
	grants := p.o.Grants
	if grants == nil {
		grants = p.m.Permissions
	}
	g := plugin.NewGrants()
	g.Grant(p.m.ID, grants)
	data := p.o.Data
	if data == "" {
		data = filepath.Join(logs.StateDir(), "plugins", p.m.ID)
	}
	vm := script.New()
	vm.OnSlow = func(src string, took time.Duration) { h.Log().Warn("slow Lua", "function", src, "took", took) }
	apis := api.InstallPlugin(vm, h, plugin.Caller{ID: p.m.ID, Dir: p.o.Dir, Guard: g, Data: data})
	if err := vm.Load(script.Chunk{Name: p.m.ID + "/" + plugin.EntryFile, Source: src}); err != nil {
		apis.Dispose()
		vm.Close()
		p.Deactivate(context.Background())
		p.failed(h, err)
		return nil
	}
	p.vm, p.apis = vm, apis
	return nil
}

func (p *scriptPlugin) failed(h plugin.Host, err error) {
	h.Notify(core.Notice{ID: "plugin.load", Severity: core.SevError, Title: "Plugin " + p.m.ID, Text: err.Error()})
}

func (p *scriptPlugin) Deactivate(context.Context) error {
	if p.contrib != nil {
		p.contrib.Dispose()
		p.contrib = nil
	}
	if p.apis != nil {
		p.apis.Dispose()
		p.vm.Close()
		p.apis, p.vm = nil, nil
	}
	return nil
}
