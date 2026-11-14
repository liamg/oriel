package ship_test

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// GitHub's code host asks GitHub's API itself, through the window's network as every plugin's requests go, with the
// site's token; the GitHub service's search refuses an empty one.
func TestGitHubHostAsksThroughTheWindowsNet(t *testing.T) {
	var mu sync.Mutex
	var asked []string
	net := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		asked = append(asked, req.URL.Host+" "+req.Header.Get("Authorization"))
		mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"data":{"search":{"nodes":[]}}}`)),
			Header: http.Header{"Content-Type": {"application/json"}}, Request: req}, nil
	})}
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret("GITHUB_TOKEN", "site-token")
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.Net, cfg.Secrets = net, secrets }})
	h, ok := registry.HostByName(s.Registry(), "github")
	if !ok || h.Requested == nil {
		t.Fatal("no GitHub code host")
	}
	ctx := context.Background()
	if _, err := h.Requested(ctx, h.Sites[0]); err != nil {
		t.Fatal(err)
	}
	g, err := kit.GitHubOf(s.Registry())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.SearchPRs(ctx, " "); err == nil {
		t.Error("an empty search ran")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(asked) == 0 {
		t.Fatalf("asked %q", asked)
	}
	for _, a := range asked {
		if a != "api.github.com Bearer site-token" {
			t.Errorf("asked %q, not GitHub's API with the site's token", a)
		}
	}
}
