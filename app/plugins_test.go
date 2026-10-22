package app_test

import (
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"
)

// settingsPlugin is the settings plugin, which reads settings.toml.
func settingsPlugin(s *apptest.Scene) *settings.Plugin {
	return apptest.PluginOf[*settings.Plugin](s, "settings")
}
