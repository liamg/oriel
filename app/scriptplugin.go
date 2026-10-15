package app

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"
)

// scriptPlugin is a Lua plugin run as an extension: its oriel.toml and init.lua, in a VM of its own whose every
// call is checked against the permissions the manifest declares (security.md).
type scriptPlugin struct {
	fsys fs.FS
	m    ext.Manifest
	err  error
	vm   *script.VM
	apis *api.APIs
}

// ScriptPlugin is the Lua plugin whose oriel.toml and init.lua are at fsys's root, granted what its manifest
// declares: one shipped with this build, whose consent is the build's. A plugin that fails to load says so in a
// notice and leaves the app running.
func ScriptPlugin(fsys fs.FS) ext.Extension {
	p := &scriptPlugin{fsys: fsys}
	b, err := fs.ReadFile(fsys, ext.ManifestFile)
	if err == nil {
		p.m, err = ext.ParseManifest(b)
	}
	if p.m.ID == "" {
		p.m.ID = "plugin"
	}
	p.err = err
	return p
}

func (p *scriptPlugin) Manifest() ext.Manifest { return p.m }

func (p *scriptPlugin) Activate(_ context.Context, h ext.Host) error {
	if p.err != nil {
		p.failed(h, p.err)
		return nil
	}
	src, err := fs.ReadFile(p.fsys, "init.lua")
	if err != nil {
		p.failed(h, err)
		return nil
	}
	g := ext.NewGrants()
	g.Grant(p.m.ID, p.m.Permissions)
	vm := script.New()
	vm.OnSlow = func(src string, took time.Duration) { h.Log().Warn("slow Lua", "function", src, "took", took) }
	apis := api.InstallPlugin(vm, h, ext.Plugin{ID: p.m.ID, Guard: g, Data: filepath.Join(logs.StateDir(), "plugins", p.m.ID)})
	if err := vm.Load(script.Chunk{Name: p.m.ID + "/init.lua", Source: src}); err != nil {
		apis.Dispose()
		vm.Close()
		p.failed(h, err)
		return nil
	}
	p.vm, p.apis = vm, apis
	return nil
}

func (p *scriptPlugin) failed(h ext.Host, err error) {
	h.Notify(core.Notice{ID: "plugin.load", Severity: core.SevError, Title: "Plugin " + p.m.ID, Text: err.Error()})
}

func (p *scriptPlugin) Deactivate(context.Context) error {
	if p.apis != nil {
		p.apis.Dispose()
		p.vm.Close()
		p.apis, p.vm = nil, nil
	}
	return nil
}
