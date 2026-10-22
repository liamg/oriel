package settings_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/settings"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/registry"
)

// pluginRepo is a git repository holding a plugin's files, as a git source names it.
func pluginRepo(t *testing.T, files map[string]string) (dir, commit string) {
	t.Helper()
	dir = rig.PluginDir(t, files)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "v1"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// catalogScene is the app with a data directory of the test's, and the user's settings.toml and lockfile paths.
func catalogScene(t *testing.T) (s *apptest.Scene, settingsFile, lock, data string) {
	t.Helper()
	data = rig.WritableAfter(t, t.TempDir())
	s = apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.DataDir = data }})
	return s, filepath.Join(s.ConfigDir, "settings.toml"), filepath.Join(s.ConfigDir, catalog.LockFile), data
}

func catalogOf(s *apptest.Scene) *settings.Catalog {
	return apptest.PluginOf[*settings.Catalog](s, "catalog")
}

// syncPlugins reads the settings files again and waits for the plugin manager to sync what they say.
func syncPlugins(s *apptest.Scene) {
	s.T.Helper()
	settingsOf(s).Reload()
	synced := catalogOf(s).Sync()
	if !s.Headless.Until(30*time.Second, func() bool {
		select {
		case <-synced:
			return true
		default:
			return false
		}
	}) {
		s.T.Fatal("the plugin manager didn't sync")
	}
}

const hiPlugin = `local L = oriel
L.command("Hi", function() L.notify("hi") end)
`

// Adding a [plugins] line installs the plugin. Removing it deactivates the plugin, removes its files and drops its
// lock entry.
func TestPluginsLineInstallsAndUninstalls(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	s, settings, lock, data := catalogScene(t)
	rig.WriteFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n")
	syncPlugins(s)
	if !s.HasCommand("Hi") {
		t.Fatalf("the plugin isn't loaded; notices: %v", s.Notices())
	}
	installed := filepath.Join(data, "plugins", "acme", "hi", commit)
	if _, err := os.Stat(filepath.Join(installed, "init.lua")); err != nil {
		t.Errorf("not installed at %s: %v", installed, err)
	}
	if got := rig.ReadFile(t, lock); !strings.Contains(got, `["acme/hi"]`) || !strings.Contains(got, commit) ||
		!strings.Contains(got, `badge = "unverified"`) {
		t.Errorf("lockfile:\n%s", got)
	}
	rig.WriteFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n")
	syncPlugins(s)
	if s.HasCommand("Hi") {
		t.Error("the plugin is still loaded")
	}
	if _, err := os.Stat(filepath.Join(data, "plugins", "acme", "hi")); !os.IsNotExist(err) {
		t.Errorf("its files are still there: %v", err)
	}
	if b, err := os.ReadFile(lock); err == nil && strings.Contains(string(b), "acme/hi") {
		t.Errorf("its lock entry stays:\n%s", b)
	}
}

const netPlugin = "id = \"acme/net\"\nversion = \"1.0.0\"\n[permissions]\nnet = { hosts = [\"api.example.com\"], why = \"its API\" }\n"

// With a fresh data dir and a lockfile that covers the plugin, it activates with no prompt, as dotfiles on a new
// machine need. When the plugin asks for more than the lockfile covers, it stays inactive and a card appears.
func TestLockfileConsent(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": netPlugin, "init.lua": hiPlugin})
	s, settings, lock, _ := catalogScene(t)
	rig.WriteFile(t, lock, `["acme/net"]
source       = "git:`+repo+`"
version      = "1.0.0"
commit       = "`+commit+`"
badge        = "unverified"
capabilities = ["net:api.example.com"]
`)
	rig.WriteFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	syncPlugins(s)
	if !s.HasCommand("Hi") {
		t.Fatalf("a locked plugin isn't loaded; notices: %v", s.Notices())
	}
	if _, ok := s.InboxCard("plugins", "acme/net"); ok {
		t.Error("a locked plugin asks for consent")
	}

	// The lockfile accepted less than it asks for now.
	rig.WriteFile(t, settings, "")
	syncPlugins(s)
	rig.WriteFile(t, lock, `["acme/net"]
source       = "git:`+repo+`"
commit       = "`+commit+`"
capabilities = ["net:other.example.com"]
`)
	rig.WriteFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	syncPlugins(s)
	if s.HasCommand("Hi") {
		t.Error("a plugin asking for more than its lock entry is loaded")
	}
	card, ok := s.InboxCard("plugins", "acme/net")
	if !ok {
		t.Fatal("no card for the plugin waiting for consent")
	}
	if len(card.Actions) == 0 || card.Actions[0].Command != "plugins.review" {
		t.Fatalf("the card's actions: %+v", card.Actions)
	}
	s.Run(card.Actions[0].Command, card.Actions[0].Args)
	s.Frame()
	if !strings.Contains(s.Text(), "Install acme/net") && !strings.Contains(s.Text(), "Install net") {
		t.Fatalf("the consent screen isn't up:\n%s", s.Text())
	}
	s.Run("plugins.consent.accept", nil)
	if !s.Headless.Until(30*time.Second, func() bool { return s.HasCommand("Hi") }) {
		t.Errorf("not loaded once accepted; notices: %v", s.Notices())
	}
	if got := rig.ReadFile(t, lock); !strings.Contains(got, `"net:api.example.com"`) {
		t.Errorf("the lockfile doesn't record what was accepted:\n%s", got)
	}
}

// A git source installs with the unverified badge (a git or path source), and the install screen shows "Not in any
// registry. Nobody has reviewed this code."
func TestGitSourceIsUnverified(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": netPlugin, "init.lua": hiPlugin})
	s, settings, _, _ := catalogScene(t)
	rig.WriteFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	syncPlugins(s)
	card, ok := s.InboxCard("plugins", "acme/net")
	if !ok {
		t.Fatal("no consent card for a new plugin")
	}
	if !strings.Contains(card.Sub, "unverified") || card.Note != catalog.UnverifiedWarning {
		t.Errorf("the card: sub %q, note %q", card.Sub, card.Note)
	}
	s.Run("plugins.review", registry.Args{"id": "acme/net"})
	s.Frame()
	text := s.Text()
	for _, want := range []string{"UNVERIFIED", "Not in any registry. Nobody has reviewed this code."} {
		if !strings.Contains(text, want) {
			t.Errorf("the install screen doesn't say %q:\n%s", want, text)
		}
	}
}

// A plugin option set in the GUI changes exactly one line under [plugin.<name>]: options appear in the plugin's
// section of the settings page and save to [plugin.jira].
func TestPluginOptionOneLine(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n",
		"init.lua": `oriel.option{ name = "plugin.hi.loud", type = "bool", default = false, help = "Shout" }` + "\n"})
	s, settings, _, _ := catalogScene(t)
	rig.WriteFile(t, settings, "[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n\n[plugin.hi]\n# how it greets\n")
	syncPlugins(s)
	s.Run("settings.open", registry.Args{"section": "hi"})
	s.Frame()
	s.Frame()
	before := rig.ReadFile(t, settings)
	s.Click(apptest.Centre(s.Node("plugin.hi.loud").Bounds), 0)
	after := rig.ReadFile(t, settings)
	if r, a := rig.Changed(before, after); r != 0 || a != 1 || !strings.Contains(after, "[plugin.hi]\nloud = true\n# how it greets\n") {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
}

// The GUI installs by writing one line to [plugins], then follows the same path.
func TestInstallWritesOneLine(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	s, settings, _, _ := catalogScene(t)
	rig.WriteFile(t, settings, "# mine\n[plugins]\n")
	before := rig.ReadFile(t, settings)
	s.Run("plugins.install", registry.Args{"id": "acme/hi", "git": repo})
	after := rig.ReadFile(t, settings)
	if r, a := rig.Changed(before, after); r != 0 || a != 1 {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
	syncPlugins(s)
	if !s.HasCommand("Hi") {
		t.Errorf("not loaded; notices: %v", s.Notices())
	}
	s.Run("plugins.uninstall", registry.Args{"id": "acme/hi"})
	if got := rig.ReadFile(t, settings); got != before {
		t.Errorf("uninstall left:\n%s", got)
	}
}

// A plugin from a project's .oriel/settings.toml is installed only once that project is trusted.
func TestProjectPluginsWaitForTrust(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	work := t.TempDir()
	project := filepath.Join(work, ".oriel", "settings.toml")
	rig.WriteFile(t, project, "[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n")
	data := rig.WritableAfter(t, t.TempDir())
	s := apptest.New(t, apptest.Options{WorkDir: work, Config: func(cfg *apptest.Config) { cfg.DataDir = data }})
	syncPlugins(s)
	if s.HasCommand("Hi") {
		t.Fatal("an untrusted project's plugin is loaded")
	}
	s.Run("settings.trust", registry.Args{"path": project})
	syncPlugins(s)
	if !s.HasCommand("Hi") {
		t.Errorf("not loaded once trusted; notices: %v", s.Notices())
	}
	if _, err := os.Stat(filepath.Join(work, ".oriel", catalog.LockFile)); err != nil {
		t.Errorf("the project's lockfile: %v", err)
	}
}
