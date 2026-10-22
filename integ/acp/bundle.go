// Package acp is the bundled ACP bridge: a plugin whose process serves any Agent Client Protocol agent (agent/acp)
// over the Oriel agent protocol, for the providers other plugins configure with acp = { … }.
package acp

import (
	"embed"
	"io"
	"io/fs"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/acp"
	"github.com/liamg/oriel/agent/rpcagent"
	"github.com/liamg/oriel/plugin"
)

//go:embed plugin
var files embed.FS

// Files are the plugin's oriel.toml and init.lua, at the root.
var Files, _ = fs.Sub(files, "plugin")

// Plugin is the bridge as oriel bundles it: its process is oriel's plugin-process acp, running Serve.
var Plugin = plugin.Bundled{FS: Files, Processes: map[string]string{"bin/oriel-acp": "acp"}}

// Serve serves the ACP provider each provider's config describes, over r and w (the process's stdin and stdout).
func Serve(r io.Reader, w io.Writer) error {
	return rpcagent.Serve(r, w, func(in rpcagent.Init) (agent.Provider, error) { return acp.FromConfig(in.Name, in.Config) })
}
