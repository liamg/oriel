package azure

import (
	"embed"
	"io/fs"

	"github.com/liamg/oriel/plugin"
)

//go:embed plugin
var files embed.FS

// Files are the plugin's oriel.toml, init.lua and icons, at the root.
var Files, _ = fs.Sub(files, "plugin")

// Plugin is the Azure DevOps plugin as oriel bundles it: its process is oriel's plugin-process azure, running Serve.
var Plugin = plugin.Bundled{FS: Files, Processes: map[string]string{"bin/oriel-azure": "azure"}}
