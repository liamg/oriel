package app

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/catalog"
)

// fakeRegistry is liamg/oriel-plugins on a fake GitHub: its latest release's index.json and index.sig, signed with
// key, and nothing else (no plugin has release archives).
type fakeRegistry struct {
	t   *testing.T
	key ed25519.PrivateKey
	srv *httptest.Server

	mu    sync.Mutex
	files map[string][]byte
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	r := &fakeRegistry{t: t, key: key, files: map[string][]byte{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch {
		case req.URL.Path == "/repos/"+catalog.DefaultRepo+"/releases":
			json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "index-1", "assets": []map[string]string{
				{"name": catalog.IndexFile, "url": r.srv.URL + "/asset/" + catalog.IndexFile},
				{"name": catalog.IndexSig, "url": r.srv.URL + "/asset/" + catalog.IndexSig}}}})
		case strings.HasPrefix(req.URL.Path, "/asset/"):
			w.Write(r.files[strings.TrimPrefix(req.URL.Path, "/asset/")])
		case strings.HasSuffix(req.URL.Path, "/tags"):
			w.Write([]byte("[]"))
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(r.srv.Close)
	return r
}

// publish signs and publishes an index of listings, as the registry's CI does on a merge.
func (r *fakeRegistry) publish(listings map[string]string) {
	files := map[string]string{}
	for id, src := range listings {
		files[filepath.Join(catalog.PluginsDir, id, catalog.PluginFile)] = src
	}
	ix, err := catalog.BuildIndex(writePlugin(r.t, files), catalog.DefaultRepo, time.Now())
	if err != nil {
		r.t.Fatal(err)
	}
	body, sig, err := catalog.SignIndex(r.key, ix)
	if err != nil {
		r.t.Fatal(err)
	}
	r.mu.Lock()
	r.files[catalog.IndexFile], r.files[catalog.IndexSig] = body, sig
	r.mu.Unlock()
}

// fromGitHub has git fetch github.com/<repo> from dir, as a local stand-in for the plugin's repository.
func fromGitHub(t *testing.T, repo, dir string) {
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "url."+dir+".insteadOf")
	t.Setenv("GIT_CONFIG_VALUE_0", "https://github.com/"+repo)
}

// PL-21 done when: "Installing from the Registry tab writes one line to [plugins], then follows PL-20's path", and
// "a revoked version is disabled with a card". catalog.md: "Revoked: disabled at the next index fetch, with an inbox
// card saying why."
func TestInstallFromTheRegistryTab(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\ndescription = \"Says hi\"\n", "init.lua": hiPlugin})
	fromGitHub(t, "acme/oriel-hi", repo)
	reg := newFakeRegistry(t)
	listing := "repo = \"github.com/acme/oriel-hi\"\ndescription = \"Says hi\"\n\n[[versions]]\nversion = \"1.0.0\"\ncommit = \"" + commit +
		"\"\nreviewed = \"https://github.com/liamg/oriel-plugins/pull/7\"\n"
	reg.publish(map[string]string{"acme/hi": listing})

	data := writableAfter(t, t.TempDir())
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		cfg.DataDir, cfg.GitHub = data, &catalog.GitHub{Base: reg.srv.URL}
	})
	settings := filepath.Join(s.sh.cfg.ConfigDir, "settings.toml")
	writeFile(t, settings, "[registry]\nkeys = { \""+catalog.DefaultRepo+"\" = \""+catalog.FormatKey(reg.key.Public().(ed25519.PublicKey))+"\" }\n\n[plugins]\n")
	s.settings().loadAll()

	s.run("plugins.open", map[string]any{"tab": "registry"})
	if !s.Until(30*time.Second, func() bool { return strings.Contains(s.Text(), "acme/hi") }) {
		t.Fatalf("the Registry tab doesn't list acme/hi:\n%s", s.Text())
	}
	for _, want := range []string{"Says hi", "REVIEWED", "1.0.0"} {
		if !strings.Contains(s.Text(), want) {
			t.Errorf("the registry row doesn't show %q", want)
		}
	}
	before := readFile(t, settings)
	s.click(centre(s.access("Install").Bounds))
	if !s.Until(10*time.Second, func() bool { b, _ := os.ReadFile(settings); return string(b) != before }) {
		t.Fatal("Install wrote nothing")
	}
	after := readFile(t, settings)
	if r, a := changed(before, after); r != 0 || a != 1 || !strings.Contains(after, "[plugins]\n\"acme/hi\" = \"*\"\n") {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
	s.settings().loadAll()
	if !s.Until(30*time.Second, func() bool { return s.hasCommand("Hi") }) {
		t.Fatalf("not loaded; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	lock := readFile(t, filepath.Join(s.sh.cfg.ConfigDir, catalog.LockFile))
	for _, want := range []string{`source = "registry:liamg/oriel-plugins"`, `badge = "reviewed"`, `version = "1.0.0"`, commit} {
		if !strings.Contains(lock, want) {
			t.Errorf("the lockfile doesn't have %s:\n%s", want, lock)
		}
	}

	reg.publish(map[string]string{"acme/hi": listing + "\n[revoked]\n\"1.0.0\" = \"leaked tokens\"\n"})
	s.run("plugins.refresh", nil)
	if !s.Until(30*time.Second, func() bool { _, ok := s.inboxCard("plugins", "revoked:acme/hi"); return ok && !s.hasCommand("Hi") }) {
		t.Fatalf("a revoked version is still running, or has no card; notices: %v", noticeTexts(s.store.State().Notifications))
	}
	card, _ := s.inboxCard("plugins", "revoked:acme/hi")
	if !strings.Contains(card.Detail, "leaked tokens") {
		t.Errorf("the card doesn't say why: %q", card.Detail)
	}
}

// PL-21 done when: "an index with a bad signature is refused", here as the app sees it: the registry's plugins
// aren't offered, and the tab says why.
func TestRegistryTabRefusesABadSignature(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.publish(map[string]string{"acme/hi": "repo = \"github.com/acme/oriel-hi\"\ntrusted = true\n"})
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.GitHub = &catalog.GitHub{Base: reg.srv.URL} })
	writeFile(t, filepath.Join(s.sh.cfg.ConfigDir, "settings.toml"),
		"[registry]\nkeys = { \""+catalog.DefaultRepo+"\" = \""+catalog.FormatKey(other.Public().(ed25519.PublicKey))+"\" }\n")
	s.settings().loadAll()
	s.run("plugins.open", map[string]any{"tab": "registry"})
	if !s.Until(30*time.Second, func() bool { return strings.Contains(s.Text(), "signature doesn't verify") }) {
		t.Fatalf("no word of the bad signature:\n%s", s.Text())
	}
	if strings.Contains(s.Text(), "acme/hi") {
		t.Error("a plugin from an index that doesn't verify is offered")
	}
}

// ui.md 16 · Plugins: what's installed, with its badge, what it contributes, its permissions and how it runs; the
// selected one's manifest, grants and provenance beside.
func TestPluginsPageSnapshot(t *testing.T) {
	s, settings, lock, _ := catalogScene(t)
	home, _ := os.UserHomeDir()
	writeFile(t, filepath.Join(home, "plugins", "hello", "oriel.toml"), "id = \"acme/hello\"\nversion = \"0.3.1\"\n"+
		"description = \"Greets you, and fetches the day's greeting from its API\"\n"+
		"[permissions]\nnet = { hosts = [\"api.example.com\"], why = \"the day's greeting\" }\n")
	writeFile(t, filepath.Join(home, "plugins", "hello", "init.lua"), hiPlugin+"oriel.option{ name = \"plugin.hello.loud\", type = \"bool\", default = false }\n")
	writeFile(t, filepath.Join(home, "plugins", "dusk", "oriel.toml"), "id = \"acme/dusk\"\nversion = \"1.0.0\"\n[contributes]\nthemes = [\"dusk.toml\"]\n")
	writeFile(t, filepath.Join(home, "plugins", "dusk", "dusk.toml"), "name = \"dusk\"\nextends = \"oriel\"\n")
	writeFile(t, lock, "[\"acme/hello\"]\nsource = \"path:~/plugins/hello\"\ncapabilities = [\"net:api.example.com\"]\n")
	writeFile(t, settings, "[plugins]\n\"acme/hello\" = { path = \"~/plugins/hello\" }\n\"acme/dusk\" = { path = \"~/plugins/dusk\" }\n")
	s.syncPlugins(t)
	s.run("plugins.open", map[string]any{"tab": "installed"})
	s.catalog().page.filter = "acme"
	s.frame()
	s.frame()
	s.golden("plugins")
}
