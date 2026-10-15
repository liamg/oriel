package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/registry"
)

// pluginRepo is a git repository holding a plugin's files, as a git source names it.
func pluginRepo(t *testing.T, files map[string]string) (dir, commit string) {
	t.Helper()
	dir = writePlugin(t, files)
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

// writableAfter makes dir writable again when the test ends, so its installed plugins, kept read-only, can go.
func writableAfter(t *testing.T, dir string) string {
	t.Cleanup(func() {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return dir
}

// catalogScene is the app with a data directory of the test's, and the user's settings.toml and lockfile paths.
func catalogScene(t *testing.T) (s *errScene, settings, lock, data string) {
	t.Helper()
	data = writableAfter(t, t.TempDir())
	s = newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.DataDir = data })
	return s, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"), filepath.Join(s.sh.cfg.ConfigDir, catalog.LockFile), data
}

func (s *errScene) catalog() *catalogPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*catalogPlugin); ok {
			return x
		}
	}
	return nil
}

// syncPlugins reads the settings files again and waits for the plugin manager to sync what they say.
func (s *errScene) syncPlugins(t *testing.T) {
	t.Helper()
	x := s.catalog()
	x.mu.Lock()
	synced := x.synced
	x.mu.Unlock()
	if !s.settings().loadAll() {
		x.kick()
	}
	deadline := time.After(30 * time.Second)
	for {
		select {
		case <-synced:
			s.RunQueued()
			s.frame()
			return
		case <-deadline:
			t.Fatal("the plugin manager didn't sync")
		default:
			s.RunQueued()
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func (s *errScene) hasCommand(id string) bool {
	for _, c := range s.reg.Commands() {
		if c.ID == id {
			return true
		}
	}
	return false
}

func (s *errScene) inboxCard(source, id string) (registry.InboxCard, bool) {
	for _, c := range s.reg.Contributions("inbox.source") {
		src := c.(registry.InboxSource)
		if src.Name != source {
			continue
		}
		for _, card := range src.Cards() {
			if card.ID == id {
				return card, true
			}
		}
	}
	return registry.InboxCard{}, false
}

const hiPlugin = `local L = oriel
L.command("Hi", function() L.notify("hi") end)
`

// PL-20 done when: "Adding a [plugins] line installs the plugin. Removing it uninstalls it and drops its lock
// entry." catalog.md: "If an entry is removed, the plugin is deactivated, its files are removed, and its lock entry
// is dropped."
func TestPluginsLineInstallsAndUninstalls(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	s, settings, lock, data := catalogScene(t)
	writeFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n")
	s.syncPlugins(t)
	if !s.hasCommand("Hi") {
		t.Fatalf("the plugin isn't loaded; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	installed := filepath.Join(data, "plugins", "acme", "hi", commit)
	if _, err := os.Stat(filepath.Join(installed, "init.lua")); err != nil {
		t.Errorf("not installed at %s: %v", installed, err)
	}
	if got := readFile(t, lock); !strings.Contains(got, `["acme/hi"]`) || !strings.Contains(got, commit) ||
		!strings.Contains(got, `badge = "unverified"`) {
		t.Errorf("lockfile:\n%s", got)
	}
	writeFile(t, settings, "[ui]\ntheme = \"oriel\"\n\n[plugins]\n")
	s.syncPlugins(t)
	if s.hasCommand("Hi") {
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

// PL-20 done when: "With a fresh data dir and a lockfile that covers the plugin, it activates with no prompt. When
// the plugin asks for more than the lockfile covers, it stays inactive and a card appears." catalog.md: "If the
// lockfile records that you accepted this version with these capabilities, the plugin activates silently. That's
// how dotfiles on a new machine work."
func TestLockfileConsent(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": netPlugin, "init.lua": hiPlugin})
	s, settings, lock, _ := catalogScene(t)
	writeFile(t, lock, `["acme/net"]
source       = "git:`+repo+`"
version      = "1.0.0"
commit       = "`+commit+`"
badge        = "unverified"
capabilities = ["net:api.example.com"]
`)
	writeFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	s.syncPlugins(t)
	if !s.hasCommand("Hi") {
		t.Fatalf("a locked plugin isn't loaded; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	if _, ok := s.inboxCard("plugins", "acme/net"); ok {
		t.Error("a locked plugin asks for consent")
	}

	// The lockfile accepted less than it asks for now.
	writeFile(t, settings, "")
	s.syncPlugins(t)
	writeFile(t, lock, `["acme/net"]
source       = "git:`+repo+`"
commit       = "`+commit+`"
capabilities = ["net:other.example.com"]
`)
	writeFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	s.syncPlugins(t)
	if s.hasCommand("Hi") {
		t.Error("a plugin asking for more than its lock entry is loaded")
	}
	card, ok := s.inboxCard("plugins", "acme/net")
	if !ok {
		t.Fatal("no card for the plugin waiting for consent")
	}
	if len(card.Actions) == 0 || card.Actions[0].Command != "plugins.review" {
		t.Fatalf("the card's actions: %+v", card.Actions)
	}
	s.run(card.Actions[0].Command, card.Actions[0].Args)
	s.frame()
	if !strings.Contains(s.Text(), "Install acme/net") && !strings.Contains(s.Text(), "Install net") {
		t.Fatalf("the consent screen isn't up:\n%s", s.Text())
	}
	s.run("plugins.consent.accept", nil)
	if !s.Until(30*time.Second, func() bool { return s.hasCommand("Hi") }) {
		t.Errorf("not loaded once accepted; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	if got := readFile(t, lock); !strings.Contains(got, `"net:api.example.com"`) {
		t.Errorf("the lockfile doesn't record what was accepted:\n%s", got)
	}
}

// PL-20 done when: "A git source installs with the unverified badge and its warning." catalog.md, Badges:
// unverified is "a git or path source", and the install screen shows "Not in any registry. Nobody has reviewed this
// code."
func TestGitSourceIsUnverified(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": netPlugin, "init.lua": hiPlugin})
	s, settings, _, _ := catalogScene(t)
	writeFile(t, settings, "[plugins]\n\"acme/net\" = { git = \""+repo+"\" }\n")
	s.syncPlugins(t)
	card, ok := s.inboxCard("plugins", "acme/net")
	if !ok {
		t.Fatal("no consent card for a new plugin")
	}
	if !strings.Contains(card.Sub, "unverified") || card.Note != catalog.UnverifiedWarning {
		t.Errorf("the card: sub %q, note %q", card.Sub, card.Note)
	}
	s.run("plugins.review", registry.Args{"id": "acme/net"})
	s.frame()
	text := s.Text()
	for _, want := range []string{"UNVERIFIED", "Not in any registry. Nobody has reviewed this code."} {
		if !strings.Contains(text, want) {
			t.Errorf("the install screen doesn't say %q:\n%s", want, text)
		}
	}
}

// PL-20 done when: "A plugin option set in the GUI changes exactly one line under [plugin.<name>]." catalog.md:
// options "appear in the plugin's section of the settings page, save to [plugin.jira]".
func TestPluginOptionOneLine(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n",
		"init.lua": `oriel.option{ name = "plugin.hi.loud", type = "bool", default = false, help = "Shout" }` + "\n"})
	s, settings, _, _ := catalogScene(t)
	writeFile(t, settings, "[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n\n[plugin.hi]\n# how it greets\n")
	s.syncPlugins(t)
	s.run("settings.open", registry.Args{"section": "hi"})
	s.frame()
	s.frame()
	before := readFile(t, settings)
	s.click(centre(s.access("plugin.hi.loud").Bounds))
	after := readFile(t, settings)
	if r, a := changed(before, after); r != 0 || a != 1 || !strings.Contains(after, "[plugin.hi]\nloud = true\n# how it greets\n") {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
}

// catalog.md: "The GUI installs by writing one line to [plugins], then follows the same path."
func TestInstallWritesOneLine(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	s, settings, _, _ := catalogScene(t)
	writeFile(t, settings, "# mine\n[plugins]\n")
	before := readFile(t, settings)
	s.run("plugins.install", registry.Args{"id": "acme/hi", "git": repo})
	after := readFile(t, settings)
	if r, a := changed(before, after); r != 0 || a != 1 {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
	s.syncPlugins(t)
	if !s.hasCommand("Hi") {
		t.Errorf("not loaded; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	s.run("plugins.uninstall", registry.Args{"id": "acme/hi"})
	if got := readFile(t, settings); got != before {
		t.Errorf("uninstall left:\n%s", got)
	}
}

// catalog.md: "A plugin from a project's .oriel/settings.toml is installed only once that project is trusted."
func TestProjectPluginsWaitForTrust(t *testing.T) {
	repo, _ := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\n", "init.lua": hiPlugin})
	work := t.TempDir()
	project := filepath.Join(work, ".oriel", "settings.toml")
	writeFile(t, project, "[plugins]\n\"acme/hi\" = { git = \""+repo+"\" }\n")
	data := writableAfter(t, t.TempDir())
	s := newSceneWith(t, "", work, mockupSize, func(cfg *Config) { cfg.DataDir = data })
	s.syncPlugins(t)
	if s.hasCommand("Hi") {
		t.Fatal("an untrusted project's plugin is loaded")
	}
	s.run("settings.trust", registry.Args{"path": project})
	s.syncPlugins(t)
	if !s.hasCommand("Hi") {
		t.Errorf("not loaded once trusted; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	if _, err := os.Stat(filepath.Join(work, ".oriel", catalog.LockFile)); err != nil {
		t.Errorf("the project's lockfile: %v", err)
	}
}
