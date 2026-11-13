package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liamg/oriel/sandbox/mcp"
)

// signInServer is an MCP endpoint that wants OAuth, with just enough metadata
// for a client to find its authorization server.
func signInServer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"resource": srv.URL + "/mcp", "authorization_servers": []string{srv.URL}})
	})
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/authorize",
			"token_endpoint": srv.URL + "/token", "registration_endpoint": srv.URL + "/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})
	mux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"client_id": "c1"})
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource"`)
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbe(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ok := Probe(context.Background(), fakeServer(t, "aws"))
	if ok.State != "ok" || ok.Tools != 3 {
		t.Errorf("stdio server: %+v", ok)
	}
	srv := signInServer(t)
	si := Probe(context.Background(), Server{Name: "linear", Type: "http", URL: srv.URL + "/mcp"})
	if si.State != "signin" {
		t.Errorf("oauth server: %+v", si)
	}
	bad := Probe(context.Background(), Server{Name: "gone", Type: "stdio", Command: "/nonexistent/mcp"})
	if bad.State != "error" || bad.Error == "" {
		t.Errorf("broken server: %+v", bad)
	}
}

func TestRecommendedParse(t *testing.T) {
	for _, r := range Recommended {
		var raw rawServer
		if err := json.Unmarshal(r.Config, &raw); err != nil {
			t.Fatalf("%s: %v", r.Name, err)
		}
		if _, ok := toServer(r.Name, "oriel", raw, nil); !ok {
			t.Errorf("%s does not make a server", r.Name)
		}
	}
}

// The callback listens where the redirect URL sent to the authorization server says, whatever the port file holds
// now: a sign-in whose port couldn't be kept still comes back to it.
func TestServeCallbackListensOnTheRedirectURLsPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	o := &mcp.OAuth{RedirectURL: fmt.Sprintf("http://127.0.0.1:%d/callback", port)}
	_, stop, err := ServeCallback(o)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("nothing listens on the redirect URL's port %d: %v", port, err)
	}
	c.Close()
}
