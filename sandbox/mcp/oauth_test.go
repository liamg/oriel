package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeAS struct {
	t    *testing.T
	base string
	mcp  *httpServer

	mu            sync.Mutex
	clients       map[string]string // client id -> redirect uri
	codes         map[string]url.Values
	access        map[string]bool
	refresh       map[string]bool
	n             int
	registrations int
	authorizes    int
	refreshes     int
}

func (a *fakeAS) resource() string { return a.base + "/mcp" }

func (a *fakeAS) revoke(refresh bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.access = map[string]bool{}
	if refresh {
		a.refresh = map[string]bool{}
	}
}

func (a *fakeAS) counts() (reg, authz, refresh int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.registrations, a.authorizes, a.refreshes
}

func (a *fakeAS) issue(w http.ResponseWriter) {
	a.n++
	at, rt := fmt.Sprintf("at-%d", a.n), fmt.Sprintf("rt-%d", a.n)
	a.access[at], a.refresh[rt] = true, true
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": at, "refresh_token": rt, "token_type": "Bearer", "expires_in": 3600})
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func (a *fakeAS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	writeJSON := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	switch r.URL.Path {
	case "/mcp":
		tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		a.mu.Lock()
		ok := a.access[tok]
		a.mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mcp", resource_metadata="`+a.base+`/.well-known/oauth-protected-resource/mcp"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		a.mcp.ServeHTTP(w, r)
	case "/.well-known/oauth-protected-resource/mcp":
		writeJSON(map[string]any{"resource": a.resource(), "authorization_servers": []string{a.base + "/tenant"}})
	case "/.well-known/oauth-authorization-server/tenant":
		writeJSON(map[string]any{
			"issuer":                           a.base + "/tenant",
			"authorization_endpoint":           a.base + "/tenant/authorize",
			"token_endpoint":                   a.base + "/tenant/token",
			"registration_endpoint":            a.base + "/tenant/register",
			"code_challenge_methods_supported": []string{"S256"},
		})
	case "/tenant/register":
		var req struct {
			ClientName   string   `json:"client_name"`
			RedirectURIs []string `json:"redirect_uris"`
			GrantTypes   []string `json:"grant_types"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.ClientName != "oriel" || len(req.RedirectURIs) != 1 || req.AuthMethod != "none" || strings.Join(req.GrantTypes, ",") != "authorization_code,refresh_token" {
			a.t.Errorf("bad registration %+v", req)
		}
		a.mu.Lock()
		a.registrations++
		id := fmt.Sprintf("client-%d", a.registrations)
		a.clients[id] = req.RedirectURIs[0]
		a.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		writeJSON(map[string]any{"client_id": id})
	case "/tenant/authorize":
		q := r.URL.Query()
		a.mu.Lock()
		redirect, known := a.clients[q.Get("client_id")]
		a.authorizes++
		a.n++
		code := fmt.Sprintf("code-%d", a.n)
		a.codes[code] = q
		a.mu.Unlock()
		if !known || q.Get("redirect_uri") != redirect || q.Get("response_type") != "code" ||
			q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
			q.Get("state") == "" || q.Get("resource") != a.resource() {
			a.t.Errorf("bad authorize request %v", q)
		}
		http.Redirect(w, r, redirect+"?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), http.StatusFound)
	case "/tenant/token":
		_ = r.ParseForm()
		f := r.PostForm
		if f.Get("resource") != a.resource() {
			a.t.Errorf("token request without resource: %v", f)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		switch f.Get("grant_type") {
		case "authorization_code":
			q := a.codes[f.Get("code")]
			delete(a.codes, f.Get("code"))
			if q == nil {
				tokenError(w, "invalid_grant")
				return
			}
			sum := sha256.Sum256([]byte(f.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
				a.t.Errorf("PKCE verifier does not match challenge")
				tokenError(w, "invalid_grant")
				return
			}
			if f.Get("client_id") != q.Get("client_id") || f.Get("redirect_uri") != q.Get("redirect_uri") {
				a.t.Errorf("token request mismatch: %v vs %v", f, q)
			}
			a.issue(w)
		case "refresh_token":
			if !a.refresh[f.Get("refresh_token")] {
				tokenError(w, "invalid_grant")
				return
			}
			delete(a.refresh, f.Get("refresh_token"))
			a.refreshes++
			a.issue(w)
		default:
			tokenError(w, "unsupported_grant_type")
		}
	default:
		http.NotFound(w, r)
	}
}

func TestOAuth(t *testing.T) {
	ctx := context.Background()
	mcpFake, _ := newHTTPServer(t, false)
	as := &fakeAS{t: t, mcp: mcpFake, clients: map[string]string{}, codes: map[string]url.Values{}, access: map[string]bool{}, refresh: map[string]bool{}}
	rs := httptest.NewServer(as)
	defer rs.Close()
	as.base = rs.URL

	o := &OAuth{ServerURL: as.resource()}
	cb := httptest.NewServer(http.HandlerFunc(o.HandleCallback))
	defer cb.Close()
	path := filepath.Join(t.TempDir(), "auth", "server.json")
	store := FileTokenStore(path)
	o.RedirectURL = cb.URL + "/callback"
	o.Store = store
	o.Authorize = func(ctx context.Context, authURL string) error {
		// Stand in for the browser: follow the redirect to the callback.
		resp, err := http.Get(authURL)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(b), "close this tab") {
			t.Errorf("callback page: %d %s", resp.StatusCode, b)
		}
		return nil
	}

	c, err := DialHTTP(ctx, as.resource(), nil, o)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if reg, authz, _ := as.counts(); reg != 1 || authz != 1 {
		t.Fatalf("registrations=%d authorizes=%d", reg, authz)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v", fi.Mode().Perm())
	}
	a, ok := store.Load()
	if !ok || a.AccessToken == "" || a.RefreshToken == "" || a.ClientID != "client-1" || a.Issuer != rs.URL+"/tenant" || a.TokenEndpoint != rs.URL+"/tenant/token" {
		t.Fatalf("stored auth %+v", a)
	}

	if _, err := c.ListTools(ctx); err != nil {
		t.Fatal(err)
	}

	// Expired access token: refreshed before use.
	a.Expiry = time.Now().Add(-time.Minute)
	if err := store.Save(a); err != nil {
		t.Fatal(err)
	}
	if _, err := c.CallTool(ctx, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, authz, refreshes := as.counts(); authz != 1 || refreshes != 1 {
		t.Fatalf("after expiry: authorizes=%d refreshes=%d", authz, refreshes)
	}

	// Revoked access token: 401, refresh, retry.
	as.revoke(false)
	if _, err := c.CallTool(ctx, "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, authz, refreshes := as.counts(); authz != 1 || refreshes != 2 {
		t.Fatalf("after 401: authorizes=%d refreshes=%d", authz, refreshes)
	}

	// Refresh token revoked too: full flow again, reusing the registration.
	as.revoke(true)
	if _, err := c.CallTool(ctx, "x", nil); err != nil {
		t.Fatal(err)
	}
	if reg, authz, _ := as.counts(); reg != 1 || authz != 2 {
		t.Fatalf("after full revoke: registrations=%d authorizes=%d", reg, authz)
	}
}

func TestOAuthCallbackUnknownState(t *testing.T) {
	o := &OAuth{}
	w := httptest.NewRecorder()
	o.HandleCallback(w, httptest.NewRequest(http.MethodGet, "/callback?state=nope&code=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d", w.Code)
	}
}

func TestOAuthDiscoveryFallback(t *testing.T) {
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	defer srv.Close()
	mux.HandleFunc("/.well-known/oauth-protected-resource/v1/mcp", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"authorization_servers": []string{srv.URL}, "scopes_supported": []string{"read", "write"}})
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/tok"})
	})
	o := &OAuth{ServerURL: srv.URL + "/v1/mcp"}
	md, scope, err := o.discover(context.Background(), `Bearer error="invalid_token"`)
	if err != nil {
		t.Fatal(err)
	}
	if md.AuthorizationEndpoint != srv.URL+"/auth" || md.TokenEndpoint != srv.URL+"/tok" || scope != "read write" {
		t.Fatalf("discovered %+v scope %q", md, scope)
	}
}

func TestAuthParams(t *testing.T) {
	got := authParams(`Bearer realm="x", error=invalid_token, resource_metadata="https://h/a\"b", Scope="a b"`)
	want := map[string]string{"realm": "x", "error": "invalid_token", "resource_metadata": `https://h/a"b`, "scope": "a b"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}
