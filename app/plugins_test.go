package app_test

import (
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"
)

// settingsPlugin is the settings plugin, which reads settings.toml.
func settingsPlugin(s *apptest.Scene) *settings.Settings {
	return apptest.PluginOf[*settings.Settings](s, "settings")
}
