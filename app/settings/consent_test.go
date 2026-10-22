package settings_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
)

// jiraToml is a plugin manifest asking for several capabilities.
const jiraToml = `id = "acme/oriel-jira"
version = "0.4.2"
runs = "both"

[permissions]
net     = { hosts = ["acme.atlassian.net"], why = "Fetch and update tickets on your Jira site" }
secrets = { JIRA_TOKEN = "acme.atlassian.net", why = "Authenticate to your Jira" }
"editor.read" = { why = "Spot ticket keys like ENG-2231 in branches and commits" }
"agent.tools" = { tools = ["jira_ticket", "jira_comment"], why = "Let the agent read the ticket it is working on, and post progress comments" }
"agent.prompt" = { why = "\"Start work\" on a ticket hands its description to the agent as the first prompt" }
gate    = { why = "Skip prompts for our read-only jira-cli commands" }
`

func pluginDir(t *testing.T, manifest string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "oriel.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// consentScene is the built-ins with test.installed, the command an installer runs once consent is given.
func consentScene(t *testing.T) (*apptest.Scene, *[]string) {
	s := apptest.New(t, apptest.Options{})
	var installed []string
	h := s.Host()
	s.Registry().Command(registry.Command{ID: "test.installed", Run: func(_ registry.Ctx, a registry.Args) error {
		h.Do(func() { installed = append(installed, a.String("dir")) })
		return nil
	}})
	return s, &installed
}

func consentOf(s *apptest.Scene) *settings.Consent {
	return apptest.PluginOf[*settings.Consent](s, "consent")
}

// settleInstall waits for the installer's command, which runs off the loop.
func settleInstall(s *apptest.Scene, installed *[]string, want int) {
	s.T.Helper()
	s.Until("the installer's command", func() bool { return len(*installed) >= want })
}

// At install, Oriel lists every capability the plugin declares beyond the always-allowed ones, each with Oriel's
// own explanation and risk level, and the plugin's reason if the manifest gives one. The choices are Install or
// Cancel.
func TestConsentDialog(t *testing.T) {
	s, installed := consentScene(t)
	dir := pluginDir(t, jiraToml)
	s.Run("plugins.consent", registry.Args{"dir": dir, "then": "test.installed", "source": "github.com/acme/oriel-jira"})
	s.Frame()
	s.Frame()
	s.Lint("the install consent")
	s.Golden("plugins-consent")
	text := s.FullText()
	for _, want := range []string{"Install oriel-jira", "acme/oriel-jira · v0.4.2 · runs: both", "NETWORK & SECRETS", "YOUR CODE & AGENTS",
		"CRITICAL", "net", "HTTPS to acme.atlassian.net only", "secrets",
		"JIRA_TOKEN → bound to acme.atlassian.net; the plugin never sees the value", "editor.read", "agent.tools", "agent.prompt",
		"gate", "AUTHOR SAYS", "Fetch and update tickets on your Jira site", "WHERE IT RUNS", "github.com/acme/oriel-jira",
		"all or nothing · 6 capabilities · 1 critical · audit log in :Plugins", "Cancel", "Install"} {
		if !strings.Contains(text, want) {
			t.Errorf("the dialog lacks %q", want)
		}
	}
	if len(*installed) != 0 {
		t.Fatal("the installer ran before consent")
	}
	s.ClickText("Install")
	settleInstall(s, installed, 1)
	if (*installed)[0] != dir {
		t.Errorf("the installer got %v, want %s", *installed, dir)
	}
	if consentOf(s).Waiting() != 0 || strings.Contains(s.FullText(), "Install oriel-jira") {
		t.Error("the dialog stayed up after Install")
	}
}

// All or nothing: Cancel, Escape, the header's esc and a click outside each grant nothing.
func TestConsentCancels(t *testing.T) {
	s, installed := consentScene(t)
	dir := pluginDir(t, jiraToml)
	for _, c := range []struct {
		what   string
		cancel func()
	}{
		{"Cancel", func() { s.ClickText("Cancel") }},
		{"Escape", func() { s.Press("<Esc>") }},
		{"esc", func() { s.ClickText("esc") }},
		{"outside", func() { s.Click(render.Point{X: 4, Y: 990}, 0) }},
		{"over settings", func() { s.Press("<Esc>") }},
	} {
		what, cancel := c.what, c.cancel
		if what == "over settings" {
			s.Run("settings.open", registry.Args{"section": "ui"})
			s.Frame()
		}
		s.Run("plugins.consent", registry.Args{"dir": dir, "then": "test.installed"})
		s.Frame()
		s.Frame()
		if consentOf(s).Waiting() != 1 {
			t.Fatalf("%s: the dialog isn't up", what)
		}
		cancel()
		s.Frame()
		if consentOf(s).Waiting() != 0 {
			t.Errorf("%s didn't close the dialog", what)
		}
		if what == "over settings" && !strings.Contains(s.FullText(), "SETTINGS") {
			t.Error("Escape on the dialog closed the settings page under it too")
		}
	}
	s.RunQueued()
	if len(*installed) != 0 {
		t.Errorf("a cancelled install ran the installer: %v", *installed)
	}

	// ⌘↵ in the dialog's key mode installs.
	s.Run("plugins.consent", registry.Args{"dir": dir, "then": "test.installed"})
	s.Frame()
	s.Frame()
	s.Press("<D-CR>")
	settleInstall(s, installed, 1)
}

// A plugin that only needs ui and theme installs with no prompt.
func TestConsentSilent(t *testing.T) {
	s, installed := consentScene(t)
	dir := pluginDir(t, "id = \"me/theme\"\nversion = \"1.0.0\"\n[permissions]\nui = true\ntheme = true\n")
	s.Run("plugins.consent", registry.Args{"dir": dir, "then": "test.installed"})
	settleInstall(s, installed, 1)
	s.Frame()
	if consentOf(s).Waiting() != 0 {
		t.Error("a plugin needing only ui and theme asked")
	}
}

// An update that adds capabilities is the same choice again: accept the update with its new list, or stay on the
// current version. Removing capabilities applies silently.
func TestConsentUpdate(t *testing.T) {
	s, installed := consentScene(t)
	old := pluginDir(t, `id = "acme/oriel-jira"
version = "0.4.1"
runs = "both"
[permissions]
net = { hosts = ["acme.atlassian.net"], why = "Fetch and update tickets on your Jira site" }
secrets = { JIRA_TOKEN = "acme.atlassian.net", why = "Authenticate to your Jira" }
"editor.read" = true
exec = ["jira-cli"]
`)
	dir := pluginDir(t, jiraToml)
	s.Run("plugins.consent", registry.Args{"dir": dir, "from": old, "then": "test.installed", "source": "github.com/acme/oriel-jira"})
	s.Frame()
	s.Frame()
	s.Lint("the update consent")
	s.Golden("plugins-consent-update")
	text := s.FullText()
	for _, want := range []string{"Update oriel-jira", "v0.4.1 → v0.4.2", "NEW", "NO LONGER ASKED FOR", "exec:jira-cli",
		"3 more than v0.4.1", "Stay on v0.4.1", "Update"} {
		if !strings.Contains(text, want) {
			t.Errorf("the update dialog lacks %q", want)
		}
	}
	s.ClickText("Stay on v0.4.1")
	s.RunQueued()
	if len(*installed) != 0 {
		t.Fatal("staying on the old version ran the update")
	}

	// Fewer capabilities: no prompt.
	fewer := pluginDir(t, "id = \"acme/oriel-jira\"\nversion = \"0.5.0\"\n[permissions]\nnet = [\"acme.atlassian.net\"]\n")
	s.Run("plugins.consent", registry.Args{"dir": fewer, "from": dir, "then": "test.installed"})
	settleInstall(s, installed, 1)
	if consentOf(s).Waiting() != 0 {
		t.Error("an update dropping capabilities asked")
	}

	if err := s.Registry().Run(t.Context(), "plugins.consent", registry.Args{"dir": dir, "from": pluginDir(t, "id = \"other/x\"\n")}); err == nil {
		t.Error("an update from another plugin's manifest was offered")
	}
}
