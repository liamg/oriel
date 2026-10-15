package slack_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/liamg/oriel/integ/slack"
	"github.com/liamg/oriel/plugin"
)

// security.md: the webhook is a secret "bound to a host", and the plugin may reach that host and no other.
func TestManifest(t *testing.T) {
	b, err := fs.ReadFile(slack.Files, plugin.ManifestFile)
	if err != nil {
		t.Fatal(err)
	}
	m, err := plugin.ParseManifest(b)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range m.Permissions {
		got = append(got, string(g.Cap)+"="+strings.Join(g.Scope, ","))
	}
	if m.ID != "oriel/slack" || strings.Join(got, " ") != "net=hooks.slack.com secrets=SLACK_WEBHOOK@hooks.slack.com" {
		t.Errorf("%s: %v", m.ID, got)
	}
	if _, err := fs.Stat(slack.Files, "init.lua"); err != nil {
		t.Error(err)
	}
}
