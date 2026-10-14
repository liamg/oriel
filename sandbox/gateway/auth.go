package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/mcp"
)

// ErrSignIn is a server that will only answer once the user signs in.
var ErrSignIn = errors.New("needs you to sign in")

// NewOAuth is the sign-in flow for an http server. authorize is handed the
// URL the user must open; nil makes the flow report ErrSignIn instead, for a
// check that must not start one.
func NewOAuth(s Server, authorize func(ctx context.Context, url string) error) *mcp.OAuth {
	o := &mcp.OAuth{
		ServerURL:   s.URL,
		Store:       mcp.FileTokenStore(filepath.Join(sandbox.Base(), "oauth", s.Name+".json")),
		RedirectURL: RedirectURL(),
	}
	o.Authorize = func(ctx context.Context, url string) error {
		if authorize == nil {
			return ErrSignIn
		}
		return authorize(ctx, url)
	}
	return o
}

// portFile keeps the sign-in callback on one port per machine, so a client
// registered with an authorization server stays valid from one sign-in to
// the next.
func portFile() string { return filepath.Join(sandbox.Base(), "oauth-port") }

func callbackPort() int {
	if b, err := os.ReadFile(portFile()); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 {
			return p
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 47913
	}
	p := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	_ = os.MkdirAll(sandbox.Base(), 0o700)
	_ = os.WriteFile(portFile(), []byte(strconv.Itoa(p)), 0o600)
	return p
}

// RedirectURL is where an authorization server sends the browser back.
func RedirectURL() string { return fmt.Sprintf("http://127.0.0.1:%d/callback", callbackPort()) }

// ServeCallback listens for the redirect for as long as one sign-in takes.
// done closes once the callback has been handled.
func ServeCallback(o *mcp.OAuth) (done <-chan struct{}, stop func(), err error) {
	var ln net.Listener
	for range 20 {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", callbackPort()))
		if err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if err != nil {
		return nil, nil, errors.New("another sign-in is in progress; try again in a moment")
	}
	ch := make(chan struct{})
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		o.HandleCallback(w, r)
		once.Do(func() { close(ch) })
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return ch, func() { _ = srv.Close() }, nil
}

// Status is how a server answered a check.
type Status struct {
	State string    `json:"state"` // ok | signin | error
	Tools int       `json:"tools,omitempty"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

// Probe connects to s and lists its tools, without starting a sign-in.
func Probe(ctx context.Context, s Server) Status {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	u := &Upstreams{Servers: []Server{s}, Auth: func(s Server) mcp.TokenSource { return NewOAuth(s, nil) }}
	defer u.Close()
	tools, errs := u.Tools(ctx)
	st := Status{At: time.Now().UTC(), State: "ok", Tools: len(tools)}
	if len(errs) > 0 {
		st.State, st.Error = "error", errs[0].Error
		if strings.Contains(errs[0].Error, ErrSignIn.Error()) {
			st.State, st.Error = "signin", ""
		}
	}
	return st
}

// Recommendation is a server oriel suggests for sandboxed agents.
type Recommendation struct {
	Name   string          `json:"name"`
	Title  string          `json:"title"`
	Why    string          `json:"why"`
	Config json.RawMessage `json:"config"`
}

// Recommended are the servers oriel offers to add. GitHub's runs locally with
// the host's existing `gh` login, so there is no token to manage.
var Recommended = []Recommendation{
	{
		Name: "github", Title: "GitHub",
		Why:    "Pull requests, reviews, issues and CI across your repositories, signed in as your gh login.",
		Config: json.RawMessage(`{"command":"sh","args":["-c","GITHUB_PERSONAL_ACCESS_TOKEN=$(gh auth token) exec docker run -i --rm -e GITHUB_PERSONAL_ACCESS_TOKEN ghcr.io/github/github-mcp-server"]}`),
	},
	{
		Name: "linear", Title: "Linear",
		Why:    "Read and update tickets. Signs in with your Linear account in the browser.",
		Config: json.RawMessage(`{"type":"http","url":"https://mcp.linear.app/mcp"}`),
	},
}
