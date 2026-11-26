// Package scripts is the bundled scripts plugin: shell code of your own, run on the session in front from the
// omnibox, with its workspace, its name and a project's checkout filled in.
package scripts

import (
	"embed"
	"io/fs"

	"github.com/liamg/oriel/plugin"
)

//go:embed plugin
var files embed.FS

// Files are the plugin's oriel.toml and init.lua, at the root.
var Files, _ = fs.Sub(files, "plugin")

// Plugin is the scripts plugin as oriel bundles it: Lua alone, with no process.
var Plugin = plugin.Bundled{FS: Files}
