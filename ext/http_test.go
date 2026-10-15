package ext_test

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
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/ext/exttest"
)

type network struct {
	c *http.Client
	s ext.Secrets
}

func (n network) Log() *slog.Logger    { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
func (network) Notify(core.Notice)     {}
func (n network) Net() *http.Client    { return n.c }
func (n network) Secrets() ext.Secrets { return n.s }

const hook = "https://hooks.slack.com/services/T000/B000/XXXXSECRET"

// security.md → "Secrets never enter the plugin": the plugin names a secret, and "the host injects [it], and only if
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
	secrets := &ext.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	n := network{exttest.Net(srv), secrets}
	ms := ext.HostMethods(n)
	g := ext.NewGrants()
	p := ext.Plugin{ID: "oriel/slack", Guard: g}
	g.Grant(p.ID, []ext.Grant{{Cap: ext.CapNet, Scope: []string{"hooks.slack.com"}},
		{Cap: ext.CapSecrets, Scope: []string{"SLACK_WEBHOOK@hooks.slack.com", "TOKEN@api.linear.app"}}})
	call := func(params map[string]any) (*ext.HTTPResponse, error) {
		raw, _ := json.Marshal(params)
		v, err := ms.Call(t.Context(), p, "http.request", raw)
		if err != nil {
			return nil, err
		}
		return v.(*ext.HTTPResponse), nil
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
	g.Grant(p.ID, append(g.Declared(p.ID), ext.Grant{Cap: ext.CapNet, Scope: []string{"evil.example"}}))
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"secrets" capability isn't declared for SLACK_WEBHOOK@evil.example`) {
		t.Errorf("a net grant doesn't carry the secret elsewhere: %v", err)
	}
	if strings.Contains(errText(call(post)), "XXXXSECRET") {
		t.Error("the error shows the secret")
	}

	// A token is sent as a bearer only to its host, and never over plain http.
	secrets.SetSecret("TOKEN", "tok-XXXXSECRET")
	g.Grant(p.ID, append(g.Declared(p.ID), ext.Grant{Cap: ext.CapNet, Scope: []string{"api.linear.app"}}))
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

	// A request carrying a secret doesn't follow a redirect.
	secrets.SetSecret("SLACK_WEBHOOK", "https://hooks.slack.com/redirect?k=XXXXSECRET")
	res, err = call(post)
	if err != nil || res.Status != http.StatusFound || len(got) != n0+2 {
		t.Errorf("a redirect: %+v %v, %d requests", res, err, len(got)-n0)
	}

	// Undeclared: no secrets grant, no net grant.
	g.Grant(p.ID, []ext.Grant{{Cap: ext.CapNet, Scope: []string{"hooks.slack.com"}}})
	secrets.SetSecret("SLACK_WEBHOOK", hook)
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"secrets" capability isn't declared`) {
		t.Errorf("without secrets: %v", err)
	}
	g.Revoke(p.ID)
	if _, err := call(post); err == nil || !strings.Contains(err.Error(), `"net" capability isn't declared`) {
		t.Errorf("without net: %v", err)
	}
}

func errText(_ *ext.HTTPResponse, err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// The secrets file is its owner's alone, and an empty value deletes a secret.
func TestSecretFile(t *testing.T) {
	f := &ext.SecretFile{Path: filepath.Join(t.TempDir(), "state", "secrets.json")}
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
