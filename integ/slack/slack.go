// Package slack is the Slack PTAL plugin: a raised pull request's Slack button posts it to your channel through an
// incoming webhook. It's a Lua plugin like any other (plugin/oriel.toml, plugin/init.lua), and never sees the
// webhook: the host sends the SLACK_WEBHOOK secret only to hooks.slack.com.
package slack

import (
	"embed"
	"io/fs"
)

//go:embed plugin
var files embed.FS

// Files are the plugin's oriel.toml and init.lua, at the root.
var Files, _ = fs.Sub(files, "plugin")
