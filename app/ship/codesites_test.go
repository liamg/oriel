package ship_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// testGitLab is a self-hostable code host, as the GitLab plugin brings one.
func testGitLab(probe func(string) bool, allowed *[]string) registry.CodeHost {
	return registry.CodeHost{Name: "gitlab", Title: "GitLab", Order: 20,
		Sites: []registry.Site{{Host: "gitlab.com", API: "https://gitlab.com/api/v4", Secret: "GITLAB_TOKEN"}},
		Path:  regexp.MustCompile(`^/?(.+?)(?:\.git)?/?$`), Takes: regexp.MustCompile(`/-/merge_requests/\d+`),
		Setup: registry.SiteSetup{API: "https://{host}/api/v4", Secret: "GITLAB_TOKEN", TokenURL: "https://{host}/-/user_settings/personal_access_tokens",
			TokenHelp: "A personal access token with the api scope.",
			Probe:     func(_ context.Context, host string) (bool, error) { return probe != nil && probe(host), nil }},
		Allow: func(host, secret, _ string) func() {
			*allowed = append(*allowed, host+" "+secret)
			return func() { *allowed = append(*allowed, "undo "+host) }
		}}
}

// "The first step that answers wins, and the answer is kept per hostname: you
// said so (code.hosts); your CLIs say so (gh's hosts.yml, glab's config); the URL's shape says so (/_git/); the server
// says so (a probe that sends no token)."
func TestCodeSitesFindTheHost(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	var allowed []string
	s.Registry().Contribute(testGitLab(func(host string) bool { return host == "gl.probed.test" }, &allowed))
	s.Registry().Contribute(registry.CodeHost{Name: "azure", Title: "Azure DevOps", Path: regexp.MustCompile(`/_git/([^/]+)$`),
		Shape: regexp.MustCompile(`/_git/`), Setup: registry.SiteSetup{API: "https://{host}/DefaultCollection"}})
	sites := sitesOf(s)
	at := func(remote string) (string, string, string) {
		t.Helper()
		hs, repo, ok := sites.Remote(remote)
		if !ok {
			return "", "", ""
		}
		return hs.Name, hs.Site.Host, repo
	}
	for remote, want := range map[string][3]string{
		"https://github.com/acme/api.git":         {"github", "github.com", "acme/api"},
		"git@github.com:acme/api.git":             {"github", "github.com", "acme/api"},
		"https://gitlab.com/acme/group/web.git":   {"gitlab", "gitlab.com", "acme/group/web"},
		"https://tfs.acme.corp/Coll/web/_git/api": {}, // its shape is Azure DevOps', but you haven't set it up
		"git@ghe.acme.corp:acme/api.git":          {},
	} {
		if n, h, r := at(remote); [3]string{n, h, r} != want {
			t.Errorf("%s: %s %s %s, want %v", remote, n, h, r, want)
		}
	}

	// A probe is let reach the server only while it asks: one that answers no takes its grant back.
	if _, ok := sites.Probe(context.Background(), "nope.acme.test"); ok {
		t.Error("a server no probe claimed was found")
	}
	if got := strings.Join(allowed, ","); !strings.Contains(got, "nope.acme.test ,undo nope.acme.test") {
		t.Errorf("a probe that answered no kept its grant: %q", allowed)
	}

	// A remote whose shape alone says it's a host's server isn't its plugin's to reach until you say so: a notice offers
	// to set it up, once.
	var offer core.Notification
	for _, n := range s.Store().State().Notifications {
		if strings.Contains(n.Text, "tfs.acme.corp") {
			offer = n
		}
	}
	if len(offer.Actions) == 0 || offer.Actions[0].Command != "code.setup" || offer.Actions[0].Args["code_host"] != "azure" {
		t.Errorf("no offer to set tfs.acme.corp up as Azure DevOps: %+v", offer)
	}
	if err := sites.Remember("tfs.acme.corp", "azure", ""); err != nil {
		t.Fatal(err)
	}
	if n, h, r := at("https://tfs.acme.corp/Coll/web/_git/api"); [3]string{n, h, r} != [3]string{"azure", "tfs.acme.corp", "api"} {
		t.Errorf("once set up: %s %s %s", n, h, r)
	}

	// gh's own config names a GitHub Enterprise server; glab's a GitLab.
	os.MkdirAll(filepath.Join(sites.Config(), "gh"), 0o755)
	os.WriteFile(filepath.Join(sites.Config(), "gh", "hosts.yml"), []byte("github.com:\n    user: you\nghe.acme.corp:\n    user: you\n    git_protocol: ssh\n"), 0o644)
	os.MkdirAll(filepath.Join(sites.Config(), "glab-cli"), 0o755)
	os.WriteFile(filepath.Join(sites.Config(), "glab-cli", "config.yml"), []byte("git_protocol: ssh\nhosts:\n  gitlab.com:\n    token: x\n  git.acme.corp:\n    api_host: git.acme.corp\n"), 0o644)
	if n, h, r := at("git@ghe.acme.corp:acme/api.git"); n != "github" || h != "ghe.acme.corp" || r != "acme/api" {
		t.Errorf("gh's GitHub Enterprise server: %s %s %s", n, h, r)
	}
	hs, repo, ok := sites.Remote("https://git.acme.corp/platform/api.git")
	if !ok || hs.Name != "gitlab" || repo != "platform/api" || hs.Site.API != "https://git.acme.corp/api/v4" || hs.Site.Secret != "GITLAB_TOKEN:git.acme.corp" {
		t.Errorf("glab's GitLab server: %+v %s %v", hs.Site, repo, ok)
	}
	if !strings.Contains(strings.Join(allowed, ","), "git.acme.corp GITLAB_TOKEN:git.acme.corp") {
		t.Errorf("the plugin wasn't let reach the site: %q", allowed)
	}

	// You say so, and code.api moves the API.
	s.Registry().Options().Set(ship.CodeHostsOption, registry.LayerUser, registry.At{}, map[string]string{"code.internal": "gitlab"})
	s.Registry().Options().Set(ship.CodeAPIOption, registry.LayerUser, registry.At{}, map[string]string{"code.internal": "https://code.internal/gitlab/api/v4"})
	if hs, _, ok := sites.Remote("ssh://git@code.internal:2222/team/app.git"); !ok || hs.Site.API != "https://code.internal/gitlab/api/v4" {
		t.Errorf("code.hosts and code.api: %+v %v", hs.Site, ok)
	}

	// Nothing local knows it: the probe asks the server, once, and its answer is kept.
	if _, _, ok := sites.Remote("https://gl.probed.test/a/b.git"); ok {
		t.Fatal("an unknown server was known before the probe")
	}
	if hs, ok := sites.Probe(context.Background(), "gl.probed.test"); !ok || hs.Name != "gitlab" {
		t.Fatalf("probe: %+v %v", hs, ok)
	}
	if _, _, ok := sites.Remote("https://gl.probed.test/a/b.git"); !ok {
		t.Error("the probe's answer wasn't kept")
	}
	if _, ok := sites.Probe(context.Background(), "nothing.test"); ok {
		t.Error("a server no probe knows was found")
	}

	// A pull request's URL finds its site, and the host reads its path.
	if hs, ok := sites.URL("https://git.acme.corp/platform/api/-/merge_requests/57"); !ok || hs.Key() != "gitlab@git.acme.corp" {
		t.Errorf("an MR's URL: %+v %v", hs, ok)
	}
}

// "One setup dialog per hostname, per plugin, the first time a token is needed: which host it is when
// nothing could tell, the API URL prefilled, the token with the host's instructions and Create a token ↗; remembered
// per plugin and hostname for every repo on that server."
func TestCodeSetupDialog(t *testing.T) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.Secrets = secrets }})
	var allowed []string
	s.Registry().Contribute(testGitLab(nil, &allowed))

	s.Run("code.setup", registry.Args{"hostname": "git.acme.corp"})
	s.Until("the dialog", func() bool { return strings.Contains(s.Text(), "Which code host is git.acme.corp?") })
	s.WantFrame("GitHub", "GitLab", "None of these")
	s.ClickText("GitLab")
	s.Until("GitLab's setup", func() bool { return strings.Contains(s.Text(), "A personal access token with the api scope.") })
	s.Lint("the code host setup dialog")
	s.WantFrame("API URL", "https://git.acme.corp/api/v4", "Create a token ↗", "GITLAB_TOKEN:git.acme.corp")
	s.TypeInto("GITLAB_TOKEN:git.acme.corp", "glpat-XYZ")
	s.ClickText("Save")
	s.Until("the dialog gone", func() bool { return !strings.Contains(s.Text(), "Which code host") })
	if v, ok := secrets.Secret("GITLAB_TOKEN:git.acme.corp"); !ok || v != "glpat-XYZ" {
		t.Errorf("the token: %q %v", v, ok)
	}
	hs, _, ok := kit.SitesOf(s.Registry()).Remote("git@git.acme.corp:platform/api.git")
	if !ok || hs.Name != "gitlab" || !kit.SitesOf(s.Registry()).Keyed(hs) {
		t.Fatalf("the server isn't set up: %+v %v", hs, ok)
	}
	if !strings.Contains(strings.Join(allowed, ","), "git.acme.corp GITLAB_TOKEN:git.acme.corp") {
		t.Errorf("the plugin wasn't let reach it: %q", allowed)
	}

	// None of these: never asked again, and no host there.
	s.Run("code.setup", registry.Args{"hostname": "git.other.org"})
	s.Until("the dialog", func() bool { return strings.Contains(s.Text(), "Which code host is git.other.org?") })
	s.ClickText("None of these")
	s.ClickText("Save")
	s.Until("the dialog gone", func() bool { return !strings.Contains(s.Text(), "Which code host") })
	if name, _ := kit.SitesOf(s.Registry()).Named("git.other.org"); name != "none" {
		t.Errorf("git.other.org is %q", name)
	}
}

// "A site's token comes from the first of: one you set, the environment, the CLI, then the setup
// dialog. A token the host refuses (401) is skipped for the session, and the next is tried."
func TestCodeSiteTokens(t *testing.T) {
	isolate.Test(t) // before the test's own token, which it would hide
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	t.Setenv("GITLAB_TOKEN", "from-env")
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "glab.log")
	os.WriteFile(filepath.Join(bin, "glab"), []byte("#!/bin/sh\necho \"glab $*\" >> "+log+"\necho from-glab\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.Secrets = secrets }})
	var allowed []string
	gl := testGitLab(nil, &allowed)
	gl.Tokens = registry.TokenSources{Env: []string{"GITLAB_TOKEN"}, Command: []string{"glab", "config", "get", "token", "--host", "{host}"}}
	s.Registry().Contribute(gl)
	sites := sitesOf(s)
	var ran []string
	read := func() { // the glab runs, one a line
		b, _ := os.ReadFile(log)
		ran = slices.DeleteFunc(strings.Split(strings.TrimSpace(string(b)), "\n"), func(l string) bool { return l == "" })
	}
	hs, _, ok := sites.Remote("https://gitlab.com/acme/web.git")
	if !ok {
		t.Fatal("gitlab.com isn't GitLab's")
	}
	store := s.Host().Secrets()
	token := func() string { v, _ := store.Secret("GITLAB_TOKEN"); return v }

	if got := token(); got != "from-env" {
		t.Errorf("before anything's set: %q, want the environment's", got)
	}
	sites.Warm(context.Background(), hs)
	read()
	if len(ran) != 0 {
		t.Errorf("ran %q though the environment had one", ran)
	}
	sites.Refuse(hs, errors.New("GitLab: the token was refused (HTTP 401)")) // as integ/gitlab writes it
	sites.Warm(context.Background(), hs)
	read()
	if got := token(); got != "from-glab" || len(ran) != 1 || ran[0] != "glab config get token --host gitlab.com" {
		t.Errorf("after the environment's was refused: %q, ran %q", got, ran)
	}
	sites.Refuse(hs, errors.New("GitLab: the token was refused (HTTP 401)")) // as integ/gitlab writes it
	if got := token(); got != "" || sites.Keyed(hs) {
		t.Errorf("every source refused: %q, keyed %v", got, sites.Keyed(hs))
	}
	if err := store.SetSecret("GITLAB_TOKEN", "yours"); err != nil {
		t.Fatal(err)
	}
	if got := token(); got != "yours" || !sites.Keyed(hs) {
		t.Errorf("the one you set: %q", got)
	}
	if sites.Refuse(hs, errors.New("GitLab: 500")) {
		t.Error("a 500 counted as a refusal")
	}
}

// A host's other names for its public site, its ssh hosts (ssh.dev.azure.com, ssh.github.com, altssh.gitlab.com), are
// that site: a remote on one is the site's repo, and the Reviews page lists the site once, with one token to set.
func TestCodeSiteAliases(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	loadAzure(t, s)
	loadGitlab(t, s)
	sites := sitesOf(s)
	for remote, want := range map[string][2]string{
		"git@ssh.dev.azure.com:v3/acme/web/dashboard":        {"azure@dev.azure.com", "dashboard"},
		"acme@vs-ssh.visualstudio.com:v3/acme/web/dashboard": {"azure@dev.azure.com", "dashboard"},
		"ssh://git@ssh.github.com:443/acme/api.git":          {"github@github.com", "acme/api"},
		"ssh://git@altssh.gitlab.com:443/acme/group/web.git": {"gitlab@gitlab.com", "acme/group/web"},
	} {
		hs, repo, ok := sites.Remote(remote)
		if !ok || hs.Key() != want[0] || repo != want[1] {
			t.Errorf("%s: %s %q %v, want %v", remote, hs.Key(), repo, ok, want)
		}
	}
	var keys []string
	for _, hs := range sites.ReviewSources() {
		keys = append(keys, hs.Key())
	}
	if want := []string{"github@github.com", "gitlab@gitlab.com", "azure@dev.azure.com"}; !slices.Equal(keys, want) {
		t.Errorf("review sources %q, want %q", keys, want)
	}
	s.Run("reviews.open", nil)
	s.Frame()
	text := s.Text()
	gitlab, azure := strings.Index(text, "GitLab needs a token to list your reviews"), strings.Index(text, "Azure DevOps needs a token to list your reviews")
	if n := strings.Count(text, "needs a token to list your reviews"); n != 2 || gitlab < 0 || azure < gitlab {
		t.Errorf("%d token asks, want GitLab's then Azure DevOps':\n%s", n, text)
	}

}

// docs/examples/gitea: "an error in a later callback hangs silently". A code host's answer that a later callback
// (an exec_async's, an http.request's) would have given fails with that callback's error, at once.
func TestLuaCodeHostCallbackError(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	m, err := plugin.ParseManifest([]byte("id = \"acme/broken\"\nversion = \"0.1.0\"\ndescription = \"x\"\n[permissions]\nexec = { commands = [\"true\"] }\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	src := `oriel.code.host{ name = "broken", sites = { { host = "broken.dev" } },
  requested = function(site, done)
    oriel.exec_async({ "true" }, {}, function() local rows = nil; for _ in ipairs(rows) do end; done({}) end)
  end }`
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(src)}); err != nil {
		t.Fatal(err)
	}
	h, ok := registry.HostByName(s.Registry(), "broken")
	if !ok {
		t.Fatal("the host didn't register")
	}
	got := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	go func() { _, err := h.Requested(ctx, registry.Site{Host: "broken.dev"}); got <- err }()
	s.Until("the answer", func() bool {
		select {
		case err = <-got:
			return true
		default:
			return false
		}
	})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "ipairs") {
		t.Errorf("requested = %v, want the callback's error", err)
	}
}

func sitesOf(s *apptest.Scene) *ship.Sites {
	return apptest.PluginOf[*ship.CodeSites](s, "code-sites").Sites()
}
