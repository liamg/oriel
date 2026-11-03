package gh_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/liamg/oriel/gh"
)

// noGh is a PATH with git on it and no gh, and git configs of the test's own: none, or a credential helper that
// answers password.
func noGh(t *testing.T, password string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	bin := t.TempDir()
	if err := os.Symlink(git, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	cfg := filepath.Join(home, "gitconfig")
	body := ""
	if password != "" {
		body = "[credential]\n\thelper = \"!f() { test \\\"$1\\\" = get && echo username=x-access-token; echo password=" + password + "; }; f\"\n"
	}
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	for _, v := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"} {
		t.Setenv(v, "")
	}
}

func TestTokenNeedsNoGh(t *testing.T) {
	noGh(t, "")
	if tok := gh.Token(context.Background()); tok != "" {
		t.Fatalf("no gh, no variables and no credential = %q", tok)
	}
	t.Setenv("GITHUB_TOKEN", "ghp_b")
	if tok := gh.Token(context.Background()); tok != "ghp_b" {
		t.Fatalf("GITHUB_TOKEN = %q", tok)
	}
	t.Setenv("GH_TOKEN", "ghp_a")
	if tok := gh.Token(context.Background()); tok != "ghp_a" {
		t.Fatalf("GH_TOKEN first = %q", tok)
	}
}

// With no variable and no gh, git's credential helper for the host has it; an Enterprise server reads its own
// variables, not github.com's.
func TestTokenFromGitsCredentialHelper(t *testing.T) {
	noGh(t, "ghp_git")
	if tok := gh.TokenFor(context.Background(), ""); tok != "ghp_git" {
		t.Fatalf("from git's credential helper = %q", tok)
	}
	t.Setenv("GITHUB_TOKEN", "ghp_dotcom")
	if tok := gh.TokenFor(context.Background(), "github.com"); tok != "ghp_dotcom" {
		t.Fatalf("a variable before the helper = %q", tok)
	}
	if tok := gh.TokenFor(context.Background(), "git.acme.corp"); tok != "ghp_git" {
		t.Fatalf("an Enterprise server with github.com's variable set = %q", tok)
	}
	t.Setenv("GH_ENTERPRISE_TOKEN", "ghp_ent")
	if tok := gh.TokenFor(context.Background(), "git.acme.corp"); tok != "ghp_ent" {
		t.Fatalf("GH_ENTERPRISE_TOKEN = %q", tok)
	}
}

// Tokens finds a host's token once; a host with none is asked again later, not each time.
func TestTokensFindOnce(t *testing.T) {
	asked := map[string]int{}
	tk := gh.Tokens{Find: func(_ context.Context, host string) string {
		asked[host]++
		if host == "github.com" {
			return "ghp_x"
		}
		return ""
	}}
	for range 3 {
		if tok := tk.For(context.Background(), "GitHub.com"); tok != "ghp_x" {
			t.Fatalf("token = %q", tok)
		}
		tk.For(context.Background(), "git.acme.corp")
	}
	if asked["github.com"] != 1 || asked["git.acme.corp"] != 1 {
		t.Errorf("asked %v, want each once", asked)
	}
}

// An Enterprise server's GraphQL is at /api/graphql, beside its REST API at /api/v3.
func TestHTTPEnterpriseGraphQL(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		w.Write([]byte("{}"))
	}))
	defer srv.Close()
	c := gh.HTTP{Base: srv.URL + "/api/v3", Token: "t"}
	for _, p := range []string{"graphql", "user"} {
		if err := c.Do(context.Background(), "POST", p, map[string]string{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(paths) != 2 || paths[0] != "/api/graphql" || paths[1] != "/api/v3/user" {
		t.Errorf("paths = %v", paths)
	}
}
