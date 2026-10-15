package ext

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Network is what the http.request host method sends through: the client (egress filtered, in a session's
// sandbox) and the secrets it may attach.
type Network interface {
	Net() *http.Client
	Secrets() Secrets
}

// HTTPRequest is http.request's params. A secret is named, never given: Auth is sent as a bearer token, and
// URLSecret is the whole URL (an incoming webhook), each only to the host the plugin's grant binds it to.
type HTTPRequest struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body"`
	Auth      string            `json:"auth"`
	URLSecret string            `json:"url_secret"`
	Timeout   float64           `json:"timeout"` // seconds; 30 by default
}

// HTTPResponse is http.request's answer.
type HTTPResponse struct {
	Status  int               `json:"status"`
	Body    string            `json:"body"`
	Headers map[string]string `json:"headers"`
}

// MaxHTTPBody is how much of a response http.request returns.
const MaxHTTPBody = 1 << 20

// HTTPMethod is the http.request host method: net checked against the host the request goes to, secrets against
// the host each is bound to.
func HTTPMethod(n Network) Method {
	target := func(raw json.RawMessage) string {
		var r HTTPRequest
		json.Unmarshal(raw, &r)
		u, err := requestURL(n, r)
		if err != nil {
			return ""
		}
		return u.Host
	}
	return Method{Name: "http.request", Cap: CapNet, Target: target, Run: func(ctx context.Context, p Plugin, raw json.RawMessage) (any, error) {
		var r HTTPRequest
		if err := json.Unmarshal(raw, &r); err != nil {
			return nil, err
		}
		return Fetch(ctx, n, p, r)
	}}
}

// requestURL is where r goes: its URL, or its URL secret's value.
func requestURL(n Network, r HTTPRequest) (*url.URL, error) {
	raw := r.URL
	if r.URLSecret != "" {
		if r.URL != "" {
			return nil, errors.New("http.request: give url or url_secret, not both")
		}
		v, ok := n.Secrets().Secret(r.URLSecret)
		if !ok {
			return nil, unset(r.URLSecret)
		}
		raw = v
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		if r.URLSecret != "" {
			return nil, fmt.Errorf("http.request: %s isn't an http(s) URL", r.URLSecret)
		}
		return nil, fmt.Errorf("http.request: %q isn't an http(s) URL", raw)
	}
	return u, nil
}

func unset(name string) error {
	return fmt.Errorf("the secret %s isn't set: run :Secret %s to set it", name, name)
}

// Fetch makes r's request for p once the guard allows each secret it names to go to the request's host. A
// request carrying a secret goes over https only and doesn't follow redirects; no error or answer carries the
// secret's value.
func Fetch(ctx context.Context, n Network, p Plugin, r HTTPRequest) (*HTTPResponse, error) {
	u, err := requestURL(n, r)
	if err != nil {
		return nil, err
	}
	var secrets []string
	for _, name := range []string{r.URLSecret, r.Auth} {
		if name == "" {
			continue
		}
		if err := p.Check(CapSecrets, name+"@"+u.Host); err != nil {
			return nil, err
		}
		v, ok := n.Secrets().Secret(name)
		if !ok {
			return nil, unset(name)
		}
		secrets = append(secrets, v)
	}
	if len(secrets) > 0 && u.Scheme != "https" {
		return nil, fmt.Errorf("http.request: a secret goes over https only, not to %s", u.Scheme+"://"+u.Host)
	}
	method := strings.ToUpper(r.Method)
	if method == "" {
		method = http.MethodGet
	}
	timeout := 30 * time.Second
	if r.Timeout > 0 {
		timeout = time.Duration(r.Timeout * float64(time.Second))
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), strings.NewReader(r.Body))
	if err != nil {
		return nil, fmt.Errorf("http.request: %s", hide(err.Error(), secrets))
	}
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if r.Auth != "" {
		v, _ := n.Secrets().Secret(r.Auth)
		req.Header.Set("Authorization", "Bearer "+v)
	}
	c := *n.Net()
	if len(secrets) > 0 {
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("%s %s: %s", method, u.Host, hide(err.Error(), secrets))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxHTTPBody))
	if err != nil {
		return nil, fmt.Errorf("%s %s: %s", method, u.Host, hide(err.Error(), secrets))
	}
	out := &HTTPResponse{Status: resp.StatusCode, Body: hide(string(b), secrets), Headers: map[string]string{}}
	for k := range resp.Header {
		out.Headers[strings.ToLower(k)] = hide(resp.Header.Get(k), secrets)
	}
	return out, nil
}

// hide masks each secret in s.
func hide(s string, secrets []string) string {
	for _, v := range secrets {
		if v != "" {
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	return s
}
