package gh_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// GitHub's API is asked through Clients.HTTP, with the token given, on the server named: an Enterprise server's at
// /api/v3.
func TestClientsAskThroughTheirHTTP(t *testing.T) {
	var asked []string
	c := gh.Clients{HTTP: func() *http.Client {
		return &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			asked = append(asked, req.URL.Host+req.URL.Path+" "+req.Header.Get("Authorization"))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[]`)),
				Header: http.Header{"Content-Type": {"application/json"}}, Request: req}, nil
		})}
	}}
	repo := gh.Repo{Owner: "acme", Name: "api"}
	for _, host := range []string{"", "git.acme.corp"} {
		if _, err := c.On("", nil, host, "ghp_given").PRForBranch(context.Background(), repo, "main"); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"api.github.com/repos/acme/api/pulls Bearer ghp_given", "git.acme.corp/api/v3/repos/acme/api/pulls Bearer ghp_given"}
	if !slices.Equal(asked, want) {
		t.Fatalf("asked %q, want %q", asked, want)
	}
}

// With no token given and none found, every call is refused as GitHub would refuse it, saying where to set one.
func TestClientsWithNoToken(t *testing.T) {
	noGh(t, "")
	var c gh.Clients
	_, err := c.On("", nil, "git.acme.corp", "").PRForBranch(context.Background(), gh.Repo{Owner: "acme", Name: "api"}, "main")
	var ge *gh.Error
	if !errors.As(err, &ge) || ge.Status != 401 || !strings.Contains(ge.Message, "git.acme.corp") {
		t.Fatalf("no token: %v", err)
	}
}

// Client's client, given the checkout and its env, stands in for GitHub's API; nil from it leaves the API.
func TestClientsClient(t *testing.T) {
	noGh(t, "")
	hub := ghtest.New(t)
	hub.AddPR("acme/api", gh.PR{Title: "retry", Head: "retry"})
	var got []string
	fake := true
	c := gh.Clients{Client: func(dir string, env []string) gh.Client {
		got = append(got, dir+" "+strings.Join(env, ","))
		if !fake {
			return nil
		}
		return hub.Client()
	}}
	pr, err := c.On("/w/api", []string{"GIT_DIR=x"}, "", "").PRForBranch(context.Background(), gh.Repo{Owner: "acme", Name: "api"}, "retry")
	if err != nil || pr == nil || pr.Title != "retry" {
		t.Fatalf("the fake's PR: %+v, %v", pr, err)
	}
	fake = false
	if _, err := c.On("/w/api", nil, "", "").PRForBranch(context.Background(), gh.Repo{Owner: "acme", Name: "api"}, "retry"); err == nil {
		t.Error("no fake and no token: GitHub's API answered")
	}
	if !slices.Equal(got, []string{"/w/api GIT_DIR=x", "/w/api "}) {
		t.Errorf("Client was asked for %q", got)
	}
}
