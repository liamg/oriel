package mcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TokenSource supplies bearer tokens to the HTTP transport.
type TokenSource interface {
	// Token returns the current access token ("" if none yet).
	Token(ctx context.Context) (string, error)
	// Unauthorized is called with the token a 401 rejected and its
	// WWW-Authenticate header. It obtains a fresh token (by refresh or the
	// full flow) and returns it.
	Unauthorized(ctx context.Context, rejected, wwwAuthenticate string) (string, error)
}

// TokenStore persists an OAuth client registration and its tokens.
type TokenStore interface {
	Load() (StoredAuth, bool)
	Save(StoredAuth) error
}

// StoredAuth is the persisted OAuth state for one MCP server.
type StoredAuth struct {
	ClientID             string    `json:"client_id,omitempty"`
	ClientSecret         string    `json:"client_secret,omitempty"`
	AccessToken          string    `json:"access_token,omitempty"`
	RefreshToken         string    `json:"refresh_token,omitempty"`
	Expiry               time.Time `json:"expiry,omitzero"`
	TokenEndpoint        string    `json:"token_endpoint,omitempty"`
	AuthEndpoint         string    `json:"auth_endpoint,omitempty"`
	RegistrationEndpoint string    `json:"registration_endpoint,omitempty"`
	Issuer               string    `json:"issuer,omitempty"`
}

func (a StoredAuth) expired() bool {
	return !a.Expiry.IsZero() && time.Now().Add(30*time.Second).After(a.Expiry)
}

// FileTokenStore keeps StoredAuth as a 0600 JSON file.
func FileTokenStore(path string) TokenStore { return fileStore(path) }

type fileStore string

func (p fileStore) Load() (StoredAuth, bool) {
	var a StoredAuth
	b, err := os.ReadFile(string(p))
	if err != nil || json.Unmarshal(b, &a) != nil {
		return StoredAuth{}, false
	}
	return a, true
}

func (p fileStore) Save(a StoredAuth) error {
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(string(p))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".auth-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), string(p))
}

// OAuth is a TokenSource implementing the MCP authorization spec: discovery,
// dynamic client registration, and authorization code with PKCE.
type OAuth struct {
	ServerURL   string // the MCP endpoint
	RedirectURL string // callback the caller routes to HandleCallback
	Store       TokenStore
	// Authorize is given the URL the user must open and returns once they
	// have been sent there. The flow then waits for HandleCallback.
	Authorize func(ctx context.Context, authURL string) error
	HTTP      *http.Client // nil = http.DefaultClient

	flow sync.Mutex

	mu      sync.Mutex
	pending map[string]*pendingFlow
	mem     StoredAuth
}

type pendingFlow struct {
	verifier string
	auth     StoredAuth
	done     chan flowResult
}

type flowResult struct {
	token string
	err   error
}

// flowTimeout bounds how long the full flow waits for the user.
const flowTimeout = 10 * time.Minute

// Token returns the stored access token, refreshing it first if expired.
func (o *OAuth) Token(ctx context.Context) (string, error) {
	a, _ := o.load()
	if a.AccessToken == "" || !a.expired() || a.RefreshToken == "" {
		return a.AccessToken, nil
	}
	o.flow.Lock()
	defer o.flow.Unlock()
	a, _ = o.load()
	if a.expired() && a.RefreshToken != "" {
		if na, err := o.refresh(ctx, a); err == nil {
			return na.AccessToken, nil
		}
	}
	return a.AccessToken, nil
}

// Unauthorized refreshes the token, or failing that runs the full flow. A
// token newer than the one rejected (another request's refresh) is used as is.
func (o *OAuth) Unauthorized(ctx context.Context, rejected, wwwAuthenticate string) (string, error) {
	o.flow.Lock()
	defer o.flow.Unlock()
	a, _ := o.load()
	if a.AccessToken != "" && a.AccessToken != rejected {
		return a.AccessToken, nil
	}
	if a.RefreshToken != "" && a.TokenEndpoint != "" {
		if na, err := o.refresh(ctx, a); err == nil {
			return na.AccessToken, nil
		}
	}
	return o.authorize(ctx, wwwAuthenticate)
}

// HandleCallback completes the pending flow matching ?state=.
func (o *OAuth) HandleCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	o.mu.Lock()
	p := o.pending[q.Get("state")]
	delete(o.pending, q.Get("state"))
	o.mu.Unlock()
	if p == nil {
		callbackPage(w, http.StatusBadRequest, "This sign-in link is unknown or has expired.")
		return
	}
	if e := q.Get("error"); e != "" {
		err := fmt.Errorf("mcp: authorization denied: %s %s", e, q.Get("error_description"))
		p.done <- flowResult{err: err}
		callbackPage(w, http.StatusBadRequest, "Authorization failed: "+strings.TrimSpace(e+" "+q.Get("error_description")))
		return
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {q.Get("code")},
		"redirect_uri":  {o.RedirectURL},
		"code_verifier": {p.verifier},
		"resource":      {o.ServerURL},
	}
	a, err := o.token(r.Context(), p.auth, form)
	if err != nil {
		p.done <- flowResult{err: err}
		callbackPage(w, http.StatusBadGateway, "Authorization failed: "+err.Error())
		return
	}
	p.done <- flowResult{token: a.AccessToken}
	callbackPage(w, http.StatusOK, "Signed in. You can close this tab.")
}

func callbackPage(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<!doctype html><meta charset=utf-8><title>oriel</title><p style=\"font-family:sans-serif\">%s</p>\n", html.EscapeString(msg))
}

func (o *OAuth) load() (StoredAuth, bool) {
	if o.Store != nil {
		return o.Store.Load()
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.mem, o.mem != StoredAuth{}
}

func (o *OAuth) save(a StoredAuth) error {
	if o.Store != nil {
		return o.Store.Save(a)
	}
	o.mu.Lock()
	o.mem = a
	o.mu.Unlock()
	return nil
}

func (o *OAuth) httpClient() *http.Client {
	if o.HTTP != nil {
		return o.HTTP
	}
	return http.DefaultClient
}

func (o *OAuth) refresh(ctx context.Context, a StoredAuth) (StoredAuth, error) {
	return o.token(ctx, a, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {a.RefreshToken},
		"resource":      {o.ServerURL},
	})
}

// token calls the token endpoint and stores the tokens it returns.
func (o *OAuth) token(ctx context.Context, a StoredAuth, form url.Values) (StoredAuth, error) {
	form.Set("client_id", a.ClientID)
	if a.ClientSecret != "" {
		form.Set("client_secret", a.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return a, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.httpClient().Do(req)
	if err != nil {
		return a, err
	}
	defer func() { _ = resp.Body.Close() }()
	var tr struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		ExpiresIn        int64  `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(b, &tr)
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.Error != "" {
			return a, fmt.Errorf("mcp: token endpoint: %s %s", tr.Error, tr.ErrorDescription)
		}
		return a, fmt.Errorf("mcp: token endpoint: http %d", resp.StatusCode)
	}
	a.AccessToken = tr.AccessToken
	if tr.RefreshToken != "" {
		a.RefreshToken = tr.RefreshToken
	}
	a.Expiry = time.Time{}
	if tr.ExpiresIn > 0 {
		a.Expiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	}
	return a, o.save(a)
}

// authorize runs the full flow: discovery, registration, then the user's
// consent via Authorize and HandleCallback.
func (o *OAuth) authorize(ctx context.Context, wwwAuthenticate string) (string, error) {
	if o.Authorize == nil {
		return "", errors.New("mcp: server requires authorization")
	}
	ctx, cancel := context.WithTimeout(ctx, flowTimeout)
	defer cancel()

	md, scope, err := o.discover(ctx, wwwAuthenticate)
	if err != nil {
		return "", err
	}
	a, _ := o.load()
	if a.Issuer != "" && a.Issuer != md.Issuer {
		a.ClientID, a.ClientSecret = "", ""
	}
	a.Issuer = md.Issuer
	a.AuthEndpoint = md.AuthorizationEndpoint
	a.TokenEndpoint = md.TokenEndpoint
	a.RegistrationEndpoint = md.RegistrationEndpoint
	a.AccessToken, a.RefreshToken, a.Expiry = "", "", time.Time{}
	if a.ClientID == "" {
		if a.ClientID, a.ClientSecret, err = o.register(ctx, a.RegistrationEndpoint); err != nil {
			return "", err
		}
	}
	if err := o.save(a); err != nil {
		return "", err
	}

	verifier, state := randString(), randString()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {a.ClientID},
		"redirect_uri":          {o.RedirectURL},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"state":                 {state},
		"resource":              {o.ServerURL},
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	authURL, err := url.Parse(a.AuthEndpoint)
	if err != nil {
		return "", err
	}
	for k, vs := range authURL.Query() {
		if !q.Has(k) {
			q[k] = vs
		}
	}
	authURL.RawQuery = q.Encode()

	p := &pendingFlow{verifier: verifier, auth: a, done: make(chan flowResult, 1)}
	o.mu.Lock()
	if o.pending == nil {
		o.pending = map[string]*pendingFlow{}
	}
	o.pending[state] = p
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.pending, state)
		o.mu.Unlock()
	}()

	if err := o.Authorize(ctx, authURL.String()); err != nil {
		return "", err
	}
	select {
	case r := <-p.done:
		return r.token, r.err
	case <-ctx.Done():
		return "", fmt.Errorf("mcp: waiting for authorization: %w", ctx.Err())
	}
}

type asMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint"`
}

// discover finds the authorization server (RFC 9728, then RFC 8414) and the
// scope to request.
func (o *OAuth) discover(ctx context.Context, wwwAuthenticate string) (asMetadata, string, error) {
	params := authParams(wwwAuthenticate)
	scope := params["scope"]

	u, err := url.Parse(o.ServerURL)
	if err != nil {
		return asMetadata{}, "", err
	}
	origin := u.Scheme + "://" + u.Host
	var prmURLs []string
	if s := params["resource_metadata"]; s != "" {
		prmURLs = []string{s}
	} else {
		if p := strings.TrimSuffix(u.Path, "/"); p != "" {
			prmURLs = append(prmURLs, origin+"/.well-known/oauth-protected-resource"+p)
		}
		prmURLs = append(prmURLs, origin+"/.well-known/oauth-protected-resource")
	}
	var prm struct {
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	issuer := ""
	for _, pu := range prmURLs {
		if o.getJSON(ctx, pu, &prm) == nil && len(prm.AuthorizationServers) > 0 {
			issuer = prm.AuthorizationServers[0]
			break
		}
	}
	if issuer == "" {
		issuer = origin
	}
	if scope == "" {
		scope = strings.Join(prm.ScopesSupported, " ")
	}
	md, err := o.asMetadata(ctx, issuer)
	return md, scope, err
}

func (o *OAuth) asMetadata(ctx context.Context, issuer string) (asMetadata, error) {
	u, err := url.Parse(issuer)
	if err != nil {
		return asMetadata{}, err
	}
	origin := u.Scheme + "://" + u.Host
	p := strings.TrimSuffix(u.Path, "/")
	var candidates []string
	if p != "" {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server" + p,
			origin + "/.well-known/openid-configuration" + p,
			origin + p + "/.well-known/openid-configuration",
		}
	} else {
		candidates = []string{
			origin + "/.well-known/oauth-authorization-server",
			origin + "/.well-known/openid-configuration",
		}
	}
	for _, c := range candidates {
		var md asMetadata
		if o.getJSON(ctx, c, &md) == nil && md.AuthorizationEndpoint != "" && md.TokenEndpoint != "" {
			if md.Issuer == "" {
				md.Issuer = issuer
			}
			return md, nil
		}
	}
	// No metadata: the 2025-03-26 default endpoints.
	return asMetadata{
		Issuer:                issuer,
		AuthorizationEndpoint: origin + "/authorize",
		TokenEndpoint:         origin + "/token",
		RegistrationEndpoint:  origin + "/register",
	}, nil
}

// register performs dynamic client registration (RFC 7591).
func (o *OAuth) register(ctx context.Context, endpoint string) (id, secret string, err error) {
	if endpoint == "" {
		return "", "", errors.New("mcp: authorization server supports no client registration")
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                "oriel",
		"redirect_uris":              []string{o.RedirectURL},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.httpClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if err := statusErr(resp); err != nil {
		return "", "", fmt.Errorf("mcp: client registration: %w", err)
	}
	var r struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", "", err
	}
	if r.ClientID == "" {
		return "", "", errors.New("mcp: client registration returned no client_id")
	}
	return r.ClientID, r.ClientSecret, nil
}

func (o *OAuth) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

// authParams parses the auth-params of a WWW-Authenticate header, ignoring
// scheme names. Keys are lower-cased.
func authParams(h string) map[string]string {
	out := map[string]string{}
	i := 0
	for i < len(h) {
		for i < len(h) && (h[i] == ' ' || h[i] == ',' || h[i] == '\t') {
			i++
		}
		start := i
		for i < len(h) && h[i] != '=' && h[i] != ' ' && h[i] != ',' {
			i++
		}
		key := strings.ToLower(h[start:i])
		if i >= len(h) || h[i] != '=' {
			continue
		}
		i++
		var val strings.Builder
		if i < len(h) && h[i] == '"' {
			i++
			for i < len(h) && h[i] != '"' {
				if h[i] == '\\' && i+1 < len(h) {
					i++
				}
				val.WriteByte(h[i])
				i++
			}
			i++
		} else {
			for i < len(h) && h[i] != ',' && h[i] != ' ' {
				val.WriteByte(h[i])
				i++
			}
		}
		if key != "" {
			out[key] = val.String()
		}
	}
	return out
}

func randString() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
