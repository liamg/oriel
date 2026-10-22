package plugin_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
)

type network struct {
	c *http.Client
	s plugin.Secrets
}

func (n network) Log() *slog.Logger       { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (network) Notify(core.Notice)        {}
func (n network) Net() *http.Client       { return n.c }
func (n network) Secrets() plugin.Secrets { return n.s }

const hook = "https://hooks.slack.com/services/T000/B000/XXXXSECRET"

// Secrets never enter the plugin: the plugin names a secret, and "the host injects [it], and only if
// the request goes to" the host it's bound to. A webhook's URL is its secret, so the plugin names it as the URL.
func TestHTTPSecretsStayWithTheHost(t *testing.T) {
	var got []*http.Request
	var bodies []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got, bodies = append(got, r), append(bodies, string(b))
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	n := network{hosttest.Net(srv), secrets}
	ms := plugin.HostMethods(n)
	g := plugin.NewGrants()
	p := plugin.Caller{ID: "oriel/slack", Guard: g}
	g.Grant(p.ID, []plugin.Grant{{Cap: plugin.CapNet, Scope: []string{"hooks.slack.com"}},
		{Cap: plugin.CapSecrets, Scope: []string{"SLACK_WEBHOOK@hooks.slack.com", "TOKEN@api.linear.app"}}})
	call := func(params map[string]any) (*plugin.HTTPResponse, error) {
		raw, _ := json.Marshal(params)
		v, err := ms.Call(t.Context(), p, "http.request", raw)
		if err != nil {
			return nil, err
		}
		return v.(*plugin.HTTPResponse), nil
	}
	post := map[string]any{"method": "POST", "url_secret": "SLACK_WEBHOOK", "body": `{"text":"PTAL"}`}

	if _, err := call(post); err == nil || !strings.Contains(err.Error(), "SLACK_WEBHOOK isn't set") {
		t.Fatalf("an unset secret: %v", err)
	}
	secrets.SetSecret("SLACK_WEBHOOK", hook)
	res, err := call(post)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.Body != "ok" || got[0].URL.Path != "/services/T000/B000/XXXXSECRET" || got[0].Host != "hooks.slack.com" ||
		bodies[0] != `{"text":"PTAL"}` {
		t.Errorf("the post: %+v, %s %s %q", res, got[0].Host, got[0].URL.Path, bodies[0])
	}

	// Bound to hooks.slack.com: a webhook set to another host isn't sent there, by net or by the secret's binding.
	secrets.SetSecret("SLACK_WEBHOOK", "https://evil.example/XXXXSECRET")
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"net" capability isn't declared for evil.example`) {
		t.Errorf("sent to another host: %v", err)
	}
	g.Grant(p.ID, append(g.Declared(p.ID), plugin.Grant{Cap: plugin.CapNet, Scope: []string{"evil.example"}}))
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"secrets" capability isn't declared for SLACK_WEBHOOK@evil.example`) {
		t.Errorf("a net grant doesn't carry the secret elsewhere: %v", err)
	}
	if strings.Contains(errText(call(post)), "XXXXSECRET") {
		t.Error("the error shows the secret")
	}

	// A token is sent as a bearer only to its host, and never over plain http.
	secrets.SetSecret("TOKEN", "tok-XXXXSECRET")
	g.Grant(p.ID, append(g.Declared(p.ID), plugin.Grant{Cap: plugin.CapNet, Scope: []string{"api.linear.app"}}))
	if _, err := call(map[string]any{"url": "https://evil.example/x", "auth": "TOKEN"}); err == nil {
		t.Error("a bearer token sent to another host")
	}
	if _, err := call(map[string]any{"url": "http://api.linear.app/graphql", "auth": "TOKEN"}); err == nil || !strings.Contains(err.Error(), "https only") {
		t.Errorf("a token over http: %v", err)
	}
	n0 := len(got)
	if _, err := call(map[string]any{"url": "https://api.linear.app/graphql", "auth": "TOKEN"}); err != nil {
		t.Fatal(err)
	}
	if a := got[n0].Header.Get("Authorization"); a != "Bearer tok-XXXXSECRET" {
		t.Errorf("the header: %q", a)
	}
	// Linear's personal API keys go in the header as they are (auth_scheme = "none").
	if _, err := call(map[string]any{"url": "https://api.linear.app/graphql", "auth": "TOKEN", "auth_scheme": "none"}); err != nil {
		t.Fatal(err)
	}
	if a := got[n0+1].Header.Get("Authorization"); a != "tok-XXXXSECRET" {
		t.Errorf("the header with no scheme: %q", a)
	}
	// Azure DevOps' tokens are HTTP Basic's password (auth_scheme = "basic").
	if _, err := call(map[string]any{"url": "https://api.linear.app/graphql", "auth": "TOKEN", "auth_scheme": "basic"}); err != nil {
		t.Fatal(err)
	}
	if a := got[n0+2].Header.Get("Authorization"); a != "Basic OnRvay1YWFhYU0VDUkVU" {
		t.Errorf("the header as Basic: %q", a)
	}
	n0 += 2

	// A request carrying a secret doesn't follow a redirect.
	secrets.SetSecret("SLACK_WEBHOOK", "https://hooks.slack.com/redirect?k=XXXXSECRET")
	res, err = call(post)
	if err != nil || res.Status != http.StatusFound || len(got) != n0+2 {
		t.Errorf("a redirect: %+v %v, %d requests", res, err, len(got)-n0)
	}

	// Undeclared: no secrets grant, no net grant.
	g.Grant(p.ID, []plugin.Grant{{Cap: plugin.CapNet, Scope: []string{"hooks.slack.com"}}})
	secrets.SetSecret("SLACK_WEBHOOK", hook)
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"secrets" capability isn't declared`) {
		t.Errorf("without secrets: %v", err)
	}
	g.Revoke(p.ID)
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"net" capability isn't declared`) {
		t.Errorf("without net: %v", err)
	}
}

func errText(_ *plugin.HTTPResponse, err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The secrets file is its owner's alone, and an empty value deletes a secret.
func TestSecretFile(t *testing.T) {
	f := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "state", "secrets.json")}
	if err := f.SetSecret("A", "1"); err != nil {
		t.Fatal(err)
	}
	f.SetSecret("B", "2")
	if v, ok := f.Secret("A"); !ok || v != "1" {
		t.Errorf("A = %q %v", v, ok)
	}
	if got := strings.Join(f.SecretNames(), ","); got != "A,B" {
		t.Errorf("names %s", got)
	}
	f.SetSecret("A", "")
	if _, ok := f.Secret("A"); ok {
		t.Error("A wasn't deleted")
	}
	if fi, err := statMode(f.Path); err != nil || fi&0o077 != 0 {
		t.Errorf("mode %v %v", fi, err)
	}
}

func statMode(path string) (os.FileMode, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Mode().Perm(), nil
}

// net is checked against the host a request goes to, so a redirect to another host meets it too.
func TestHTTPRedirectMeetsTheNetGrant(t *testing.T) {
	var hosts []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host)
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, "https://evil.example/x", http.StatusFound)
		case "/home":
			http.Redirect(w, r, "https://api.linear.app/ok", http.StatusFound)
		default:
			w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()
	ms := plugin.HostMethods(network{hosttest.Net(srv), &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "s.json")}})
	g := plugin.NewGrants()
	p := plugin.Caller{ID: "acme/x", Guard: g}
	g.Grant(p.ID, []plugin.Grant{{Cap: plugin.CapNet, Scope: []string{"api.linear.app"}}})
	call := func(url string) (*plugin.HTTPResponse, error) {
		raw, _ := json.Marshal(map[string]any{"url": url})
		v, err := ms.Call(t.Context(), p, "http.request", raw)
		if err != nil {
			return nil, err
		}
		return v.(*plugin.HTTPResponse), nil
	}
	if res, err := call("https://api.linear.app/home"); err != nil || res.Body != "ok" {
		t.Errorf("a redirect within the grant: %+v, %v", res, err)
	}
	hosts = nil
	_, err := call("https://api.linear.app/away")
	if err == nil || !strings.Contains(err.Error(), `"net" capability isn't declared for evil.example`) {
		t.Errorf("a redirect to another host: %v", err)
	}
	if strings.Join(hosts, ",") != "api.linear.app" {
		t.Errorf("requests went to %v", hosts)
	}
}
