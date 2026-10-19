package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// testGitLab is a self-hostable code host, as the GitLab plugin brings one.
func testGitLab(probe func(string) bool, allowed *[]string) registry.CodeHost {
	return registry.CodeHost{Name: "gitlab", Title: "GitLab", Order: 20,
		Sites: []registry.Site{{Host: "gitlab.com", API: "https://gitlab.com/api/v4", Secret: "GITLAB_TOKEN"}},
		Path:  regexp.MustCompile(`^/?(.+?)(?:\.git)?/?$`), Takes: regexp.MustCompile(`/-/merge_requests/\d+`),
		Setup: registry.SiteSetup{API: "https://{host}/api/v4", Secret: "GITLAB_TOKEN", TokenURL: "https://{host}/-/user_settings/personal_access_tokens",
			TokenHelp: "A personal access token with the api scope.",
			Probe:     func(_ context.Context, host string) (bool, error) { return probe != nil && probe(host), nil }},
		Allow: func(host, secret string) { *allowed = append(*allowed, host+" "+secret) }}
}

// hosts.md, "Which host a repo is on": "The first step that answers wins, and the answer is kept per hostname: you
// said so (code.hosts); your CLIs say so (gh's hosts.yml, glab's config); the URL's shape says so (/_git/); the server
// says so (a probe that sends no token)."
func TestCodeSitesFindTheHost(t *testing.T) {
	s := newScene(t, "", "", mockupSize)
	var allowed []string
	s.reg.Contribute(testGitLab(func(host string) bool { return host == "gl.probed.dev" }, &allowed))
	s.reg.Contribute(registry.CodeHost{Name: "azure", Title: "Azure DevOps", Path: regexp.MustCompile(`/_git/([^/]+)$`),
		Shape: regexp.MustCompile(`/_git/`), Setup: registry.SiteSetup{API: "https://{host}/DefaultCollection"}})
	sites := s.sh.sites
	sites.config = t.TempDir()
	at := func(remote string) (string, string, string) {
		t.Helper()
		hs, repo, ok := sites.remote(remote)
		if !ok {
			return "", "", ""
		}
		return hs.Name, hs.Site.Host, repo
	}
	for remote, want := range map[string][3]string{
		"https://github.com/acme/api.git":         {"github", "github.com", "acme/api"},
		"git@github.com:acme/api.git":             {"github", "github.com", "acme/api"},
		"https://gitlab.com/acme/group/web.git":   {"gitlab", "gitlab.com", "acme/group/web"},
		"https://tfs.acme.corp/Coll/web/_git/api": {"azure", "tfs.acme.corp", "api"},
		"git@ghe.acme.corp:acme/api.git":          {},
	} {
		if n, h, r := at(remote); [3]string{n, h, r} != want {
			t.Errorf("%s: %s %s %s, want %v", remote, n, h, r, want)
		}
	}

	// gh's own config names a GitHub Enterprise server; glab's a GitLab.
	os.MkdirAll(filepath.Join(sites.config, "gh"), 0o755)
	os.WriteFile(filepath.Join(sites.config, "gh", "hosts.yml"), []byte("github.com:\n    user: you\nghe.acme.corp:\n    user: you\n    git_protocol: ssh\n"), 0o644)
	os.MkdirAll(filepath.Join(sites.config, "glab-cli"), 0o755)
	os.WriteFile(filepath.Join(sites.config, "glab-cli", "config.yml"), []byte("git_protocol: ssh\nhosts:\n  gitlab.com:\n    token: x\n  git.acme.corp:\n    api_host: git.acme.corp\n"), 0o644)
	if n, h, r := at("git@ghe.acme.corp:acme/api.git"); n != "github" || h != "ghe.acme.corp" || r != "acme/api" {
		t.Errorf("gh's GitHub Enterprise server: %s %s %s", n, h, r)
	}
	hs, repo, ok := sites.remote("https://git.acme.corp/platform/api.git")
	if !ok || hs.Name != "gitlab" || repo != "platform/api" || hs.Site.API != "https://git.acme.corp/api/v4" || hs.Site.Secret != "GITLAB_TOKEN:git.acme.corp" {
		t.Errorf("glab's GitLab server: %+v %s %v", hs.Site, repo, ok)
	}
	if !strings.Contains(strings.Join(allowed, ","), "git.acme.corp GITLAB_TOKEN:git.acme.corp") {
		t.Errorf("the plugin wasn't let reach the site: %q", allowed)
	}

	// You say so, and code.api moves the API.
	s.reg.Options().Set(CodeHostsOption, registry.LayerUser, registry.At{}, map[string]string{"code.internal": "gitlab"})
	s.reg.Options().Set(CodeAPIOption, registry.LayerUser, registry.At{}, map[string]string{"code.internal": "https://code.internal/gitlab/api/v4"})
	if hs, _, ok := sites.remote("ssh://git@code.internal:2222/team/app.git"); !ok || hs.Site.API != "https://code.internal/gitlab/api/v4" {
		t.Errorf("code.hosts and code.api: %+v %v", hs.Site, ok)
	}

	// Nothing local knows it: the probe asks the server, once, and its answer is kept.
	if _, _, ok := sites.remote("https://gl.probed.dev/a/b.git"); ok {
		t.Fatal("an unknown server was known before the probe")
	}
	if hs, ok := sites.probe(context.Background(), "gl.probed.dev"); !ok || hs.Name != "gitlab" {
		t.Fatalf("probe: %+v %v", hs, ok)
	}
	if _, _, ok := sites.remote("https://gl.probed.dev/a/b.git"); !ok {
		t.Error("the probe's answer wasn't kept")
	}
	if _, ok := sites.probe(context.Background(), "nothing.dev"); ok {
		t.Error("a server no probe knows was found")
	}

	// A pull request's URL finds its site, and the host reads its path.
	if hs, ok := sites.url("https://git.acme.corp/platform/api/-/merge_requests/57"); !ok || hs.Key() != "gitlab@git.acme.corp" {
		t.Errorf("an MR's URL: %+v %v", hs, ok)
	}
}

// hosts.md: "One setup dialog per hostname, per plugin, the first time a token is needed: which host it is when
// nothing could tell, the API URL prefilled, the token with the host's instructions and Create a token ↗; remembered
// per plugin and hostname for every repo on that server."
func TestCodeSetupDialog(t *testing.T) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.Secrets = secrets })
	var allowed []string
	s.reg.Contribute(testGitLab(nil, &allowed))
	s.sh.sites.config = t.TempDir()

	s.run("code.setup", registry.Args{"hostname": "git.acme.corp"})
	s.until(t, "the dialog", func() bool { return strings.Contains(s.frameText(), "Which code host is git.acme.corp?") })
	s.wantFrame(t, "GitHub", "GitLab", "None of these")
	s.clickText(t, "GitLab")
	s.until(t, "GitLab's setup", func() bool { return strings.Contains(s.frameText(), "A personal access token with the api scope.") })
	s.lint("the code host setup dialog")
	s.wantFrame(t, "API URL", "https://git.acme.corp/api/v4", "Create a token ↗", "GITLAB_TOKEN:git.acme.corp")
	s.typeInto(t, "GITLAB_TOKEN:git.acme.corp", "glpat-XYZ")
	s.clickText(t, "Save")
	s.until(t, "the dialog gone", func() bool { return !strings.Contains(s.frameText(), "Which code host") })
	if v, ok := secrets.Secret("GITLAB_TOKEN:git.acme.corp"); !ok || v != "glpat-XYZ" {
		t.Errorf("the token: %q %v", v, ok)
	}
	hs, _, ok := s.sh.sites.remote("git@git.acme.corp:platform/api.git")
	if !ok || hs.Name != "gitlab" || !s.sh.sites.keyed(hs) {
		t.Fatalf("the server isn't set up: %+v %v", hs, ok)
	}
	if !strings.Contains(strings.Join(allowed, ","), "git.acme.corp GITLAB_TOKEN:git.acme.corp") {
		t.Errorf("the plugin wasn't let reach it: %q", allowed)
	}

	// None of these: never asked again, and no host there.
	s.run("code.setup", registry.Args{"hostname": "git.other.org"})
	s.until(t, "the dialog", func() bool { return strings.Contains(s.frameText(), "Which code host is git.other.org?") })
	s.clickText(t, "None of these")
	s.clickText(t, "Save")
	s.until(t, "the dialog gone", func() bool { return !strings.Contains(s.frameText(), "Which code host") })
	if name, _ := s.sh.sites.named("git.other.org"); name != "none" {
		t.Errorf("git.other.org is %q", name)
	}
}

// hosts.md, tokens: "A site's token comes from the first of: one you set, the environment, the CLI, then the setup
// dialog. A token the host refuses (401) is skipped for the session, and the next is tried."
func TestCodeSiteTokens(t *testing.T) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.Secrets = secrets })
	var allowed []string
	gl := testGitLab(nil, &allowed)
	gl.Tokens = registry.TokenSources{Env: []string{"GITLAB_TOKEN"}, Command: []string{"glab", "config", "get", "token", "--host", "{host}"}}
	s.reg.Contribute(gl)
	sites := s.sh.sites
	env := map[string]string{"GITLAB_TOKEN": "from-env"}
	var ran []string
	sites.env = func(k string) (string, bool) { v, ok := env[k]; return v, ok }
	sites.run = func(_ context.Context, argv []string) (string, error) {
		ran = append(ran, strings.Join(argv, " "))
		return "from-glab\n", nil
	}
	hs, _, ok := sites.remote("https://gitlab.com/acme/web.git")
	if !ok {
		t.Fatal("gitlab.com isn't GitLab's")
	}
	store := s.sh.secretStore()
	token := func() string { v, _ := store.Secret("GITLAB_TOKEN"); return v }

	if got := token(); got != "from-env" {
		t.Errorf("before anything's set: %q, want the environment's", got)
	}
	sites.warm(context.Background(), hs)
	if len(ran) != 0 {
		t.Errorf("ran %q though the environment had one", ran)
	}
	sites.refuse(hs, errors.New("GitLab: 401 Unauthorized"))
	sites.warm(context.Background(), hs)
	if got := token(); got != "from-glab" || len(ran) != 1 || ran[0] != "glab config get token --host gitlab.com" {
		t.Errorf("after the environment's was refused: %q, ran %q", got, ran)
	}
	sites.refuse(hs, errors.New("GitLab: 401 Unauthorized"))
	if got := token(); got != "" || sites.keyed(hs) {
		t.Errorf("every source refused: %q, keyed %v", got, sites.keyed(hs))
	}
	if err := store.SetSecret("GITLAB_TOKEN", "yours"); err != nil {
		t.Fatal(err)
	}
	if got := token(); got != "yours" || !sites.keyed(hs) {
		t.Errorf("the one you set: %q", got)
	}
	if sites.refuse(hs, errors.New("GitLab: 500")) {
		t.Error("a 500 counted as a refusal")
	}
}
