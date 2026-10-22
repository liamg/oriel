package settings_test

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"

	"github.com/liamg/oriel/plugin/ossandbox"
)

// TestMain fixes the time zone, and the sandbox backends this machine offers.
func TestMain(m *testing.M) {
	ossandbox.Main()
	time.Local = time.UTC
	os.Exit(m.Run())
}

// settingsOf is the scene's settings plugin.
func settingsOf(s *apptest.Scene) *settings.Plugin {
	return apptest.PluginOf[*settings.Plugin](s, "settings")
}
