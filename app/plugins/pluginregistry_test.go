package plugins_test

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

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/internal/isolate"
)

// fakeRegistry is liamg/oriel-plugins on a fake GitHub: its latest release's index.json and index.sig, signed with
// key, and nothing else (no plugin has release archives).
type fakeRegistry struct {
	t   *testing.T
	key ed25519.PrivateKey
	srv *httptest.Server

	mu    sync.Mutex
	files map[string][]byte
	tags  map[string][]fakeTag // by repo: what /repos/<repo>/tags lists
}

// fakeTag is a plugin repo's tag on the fake GitHub, and when its commit was made.
type fakeTag struct {
	name, commit string
	at           time.Time
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	isolate.Test(t)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	r := &fakeRegistry{t: t, key: key, files: map[string][]byte{}, tags: map[string][]fakeTag{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch {
		case req.URL.Path == "/repos/"+catalog.DefaultRepo+"/releases":
			if err := json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "index-1", "assets": []map[string]string{
				{"name": catalog.IndexFile, "url": r.srv.URL + "/asset/" + catalog.IndexFile},
				{"name": catalog.IndexSig, "url": r.srv.URL + "/asset/" + catalog.IndexSig}}}}); err != nil {
				t.Error(err)
			}
		case strings.HasPrefix(req.URL.Path, "/asset/"):
			if _, err := w.Write(r.files[strings.TrimPrefix(req.URL.Path, "/asset/")]); err != nil {
				t.Error(err)
			}
		case strings.HasSuffix(req.URL.Path, "/tags"):
			out := []map[string]any{}
			for _, tag := range r.tags[strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/repos/"), "/tags")] {
				out = append(out, map[string]any{"name": tag.name, "commit": map[string]string{"sha": tag.commit}})
			}
			if err := json.NewEncoder(w).Encode(out); err != nil {
				t.Error(err)
			}
		case strings.Contains(req.URL.Path, "/commits/"):
			repo, sha, _ := strings.Cut(strings.TrimPrefix(req.URL.Path, "/repos/"), "/commits/")
			for _, tag := range r.tags[repo] {
				if tag.commit == sha {
					if err := json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"committer": map[string]string{"date": tag.at.Format(time.RFC3339)}}}); err != nil {
						t.Error(err)
					}
					return
				}
			}
			http.NotFound(w, req)
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
	ix, err := catalog.BuildIndex(rig.PluginDir(r.t, files), catalog.DefaultRepo, time.Now())
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

// Installing from the Registry tab writes one line to [plugins], then follows the same path as a hand edit; a
// revoked version is disabled at the next index fetch, with an inbox card saying why.
func TestInstallFromTheRegistryTab(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\ndescription = \"Says hi\"\n", "init.lua": hiPlugin})
	fromGitHub(t, "acme/oriel-hi", repo)
	reg := newFakeRegistry(t)
	listing := "repo = \"github.com/acme/oriel-hi\"\ndescription = \"Says hi\"\n\n[[versions]]\nversion = \"1.0.0\"\ncommit = \"" + commit +
		"\"\nreviewed = \"https://github.com/liamg/oriel-plugins/pull/7\"\n"
	reg.publish(map[string]string{"acme/hi": listing})

	data := rig.WritableAfter(t, t.TempDir())
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		cfg.DataDir, cfg.GitHub = data, &catalog.GitHub{Base: reg.srv.URL}
	}})
	settings := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, settings, "[registry]\nkeys = { \""+catalog.DefaultRepo+"\" = \""+catalog.FormatKey(reg.key.Public().(ed25519.PublicKey))+"\" }\n\n[plugins]\n")
	s.Run("settings.reload", nil)

	s.Run("plugins.open", map[string]any{"tab": "registry"})
	if !s.Headless.Until(30*time.Second, func() bool { return strings.Contains(s.Text(), "acme/hi") }) {
		t.Fatalf("the Registry tab doesn't list acme/hi:\n%s", s.Text())
	}
	for _, want := range []string{"Says hi", "REVIEWED", "1.0.0"} {
		if !strings.Contains(s.Text(), want) {
			t.Errorf("the registry row doesn't show %q", want)
		}
	}
	before := rig.ReadFile(t, settings)
	s.Click(apptest.Centre(s.Node("Install").Bounds), 0)
	if !s.Headless.Until(10*time.Second, func() bool { b, _ := os.ReadFile(settings); return string(b) != before }) {
		t.Fatal("Install wrote nothing")
	}
	after := rig.ReadFile(t, settings)
	if r, a := rig.Changed(before, after); r != 0 || a != 1 || !strings.Contains(after, "[plugins]\n\"acme/hi\" = \"*\"\n") {
		t.Errorf("%d lines removed and %d added:\n%s", r, a, after)
	}
	s.Run("settings.reload", nil)
	if !s.Headless.Until(30*time.Second, func() bool { return s.HasCommand("Hi") }) {
		t.Fatalf("not loaded; notices: %v", s.Notices())
	}
	lock := rig.ReadFile(t, filepath.Join(s.ConfigDir, catalog.LockFile))
	for _, want := range []string{`source = "registry:liamg/oriel-plugins"`, `badge = "reviewed"`, `version = "1.0.0"`, commit} {
		if !strings.Contains(lock, want) {
			t.Errorf("the lockfile doesn't have %s:\n%s", want, lock)
		}
	}

	reg.publish(map[string]string{"acme/hi": listing + "\n[revoked]\n\"1.0.0\" = \"leaked tokens\"\n"})
	s.Run("plugins.refresh", nil)
	if !s.Headless.Until(30*time.Second, func() bool { _, ok := s.InboxCard("plugins", "revoked:acme/hi"); return ok && !s.HasCommand("Hi") }) {
		t.Fatalf("a revoked version is still running, or has no card; notices: %v", s.Notices())
	}
	card, _ := s.InboxCard("plugins", "revoked:acme/hi")
	if !strings.Contains(card.Detail, "leaked tokens") {
		t.Errorf("the card doesn't say why: %q", card.Detail)
	}
}

// A trusted author's plugin installs from its tags on GitHub, not reviewed versions: the newest tag whose commit has
// waited out registry.trusted_hold, pinned to that commit; a tag made since isn't offered yet.
func TestInstallATrustedAuthorsTag(t *testing.T) {
	repo, commit := pluginRepo(t, map[string]string{"oriel.toml": "id = \"acme/hi\"\nversion = \"1.0.0\"\ndescription = \"Says hi\"\n", "init.lua": hiPlugin})
	fromGitHub(t, "acme/oriel-hi", repo)
	reg := newFakeRegistry(t)
	reg.publish(map[string]string{"acme/hi": "repo = \"github.com/acme/oriel-hi\"\ndescription = \"Says hi\"\ntrusted = true\nsince = \"2026-01-01\"\n"})
	reg.mu.Lock()
	// The hold is measured on the window's clock: v1.0.0's commit is a month old, v1.1.0's an hour.
	now := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	reg.tags["acme/oriel-hi"] = []fakeTag{{"v1.0.0", commit, now.AddDate(0, -1, 0)},
		{"v1.1.0", "0123456789abcdef0123456789abcdef01234567", now.Add(-time.Hour)}}
	reg.mu.Unlock()

	data := rig.WritableAfter(t, t.TempDir())
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		cfg.DataDir, cfg.GitHub = data, &catalog.GitHub{Base: reg.srv.URL}
	}})
	s.Now = now
	settings := filepath.Join(s.ConfigDir, "settings.toml")
	rig.WriteFile(t, settings, "[registry]\nkeys = { \""+catalog.DefaultRepo+"\" = \""+catalog.FormatKey(reg.key.Public().(ed25519.PublicKey))+
		"\" }\n\n[plugins]\n\"acme/hi\" = \"*\"\n")
	s.Run("settings.reload", nil)
	if !s.Headless.Until(30*time.Second, func() bool { return s.HasCommand("Hi") }) {
		t.Fatalf("not loaded; notices: %v", s.Notices())
	}
	lock := rig.ReadFile(t, filepath.Join(s.ConfigDir, catalog.LockFile))
	for _, want := range []string{`badge = "trusted"`, `version = "1.0.0"`, commit} {
		if !strings.Contains(lock, want) {
			t.Errorf("the lockfile doesn't have %s:\n%s", want, lock)
		}
	}
	if strings.Contains(lock, "1.1.0") {
		t.Errorf("a tag inside the hold was installed:\n%s", lock)
	}
}

// An index with a bad signature is refused, here as the app sees it: the registry's plugins
// aren't offered, and the tab says why.
func TestRegistryTabRefusesABadSignature(t *testing.T) {
	reg := newFakeRegistry(t)
	reg.publish(map[string]string{"acme/hi": "repo = \"github.com/acme/oriel-hi\"\ntrusted = true\n"})
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.GitHub = &catalog.GitHub{Base: reg.srv.URL} }})
	rig.WriteFile(t, filepath.Join(s.ConfigDir, "settings.toml"),
		"[registry]\nkeys = { \""+catalog.DefaultRepo+"\" = \""+catalog.FormatKey(other.Public().(ed25519.PublicKey))+"\" }\n")
	s.Run("settings.reload", nil)
	s.Run("plugins.open", map[string]any{"tab": "registry"})
	if !s.Headless.Until(30*time.Second, func() bool { return strings.Contains(s.Text(), "signature doesn't verify") }) {
		t.Fatalf("no word of the bad signature:\n%s", s.Text())
	}
	if strings.Contains(s.Text(), "acme/hi") {
		t.Error("a plugin from an index that doesn't verify is offered")
	}
}

// The Plugins page: what's installed, with its badge, what it contributes, its permissions and how it runs; the
// selected one's manifest, grants and provenance beside.
func TestPluginsPageSnapshot(t *testing.T) {
	s, settings, lock, _ := catalogScene(t)
	home, _ := os.UserHomeDir()
	rig.WriteFile(t, filepath.Join(home, "plugins", "hello", "oriel.toml"), "id = \"acme/hello\"\nversion = \"0.3.1\"\n"+
		"description = \"Greets you, and fetches the day's greeting from its API\"\n"+
		"[permissions]\nnet = { hosts = [\"api.example.com\"], why = \"the day's greeting\" }\n")
	rig.WriteFile(t, filepath.Join(home, "plugins", "hello", "init.lua"), hiPlugin+"oriel.option{ name = \"plugin.hello.loud\", type = \"bool\", default = false }\n")
	rig.WriteFile(t, filepath.Join(home, "plugins", "dusk", "oriel.toml"), "id = \"acme/dusk\"\nversion = \"1.0.0\"\n[contributes]\nthemes = [\"dusk.toml\"]\n")
	rig.WriteFile(t, filepath.Join(home, "plugins", "dusk", "dusk.toml"), "name = \"dusk\"\nextends = \"oriel\"\n")
	rig.WriteFile(t, lock, "[\"acme/hello\"]\nsource = \"path:~/plugins/hello\"\ncapabilities = [\"net:api.example.com\"]\n")
	rig.WriteFile(t, settings, "[plugins]\n\"acme/hello\" = { path = \"~/plugins/hello\" }\n\"acme/dusk\" = { path = \"~/plugins/dusk\" }\n")
	syncPlugins(s)
	s.Run("plugins.open", map[string]any{"tab": "installed"})
	catalogOf(s).SetFilter("acme")
	s.Frame()
	s.Frame()
	s.Golden("plugins")
}
