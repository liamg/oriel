package github

import (
	"cmp"
	"context"
	"net/http"
	"strings"
	"sync"
)

// Clients make the GitHub client for a checkout or a server: Client's when it gives one (a fake, in tests), else
// GitHub's API with the token given, or the one found for the server (TokenFor). gh needn't be installed.
type Clients struct {
	// Client is the GitHub client for a checkout, given its git guard's env; nil, or nil from it, for GitHub's API.
	Client func(dir string, env []string) Client
	// HTTP is what GitHub's API is asked through; http.DefaultClient if nil, or it gives nil.
	HTTP   func() *http.Client
	tokens Tokens

	mu     sync.Mutex
	limits map[string]*Limits // each server's, by hostname
}

// Limits are the rate limits GitHub's answers have reported, of every server asked.
func (c *Clients) Limits() []Allowance {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []Allowance
	for _, l := range c.limits {
		out = append(out, l.All()...)
	}
	return out
}

func (c *Clients) limitsOf(host string) *Limits {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.limits == nil {
		c.limits = map[string]*Limits{}
	}
	if c.limits[host] == nil {
		c.limits[host] = &Limits{}
	}
	return c.limits[host]
}

// On is the client for the checkout at dir with env, on a GitHub Enterprise server's hostname when given (github.com
// if ""), with token, else the one found for that server. ctx bounds finding it: gh or git's credential helper is
// stopped when ctx ends.
func (c *Clients) On(ctx context.Context, dir string, env []string, hostname, token string) Client {
	host := strings.ToLower(cmp.Or(hostname, "github.com"))
	if c.Client != nil {
		if cl := c.Client(dir, env); cl != nil {
			if api, ok := cl.(API); ok {
				return api.limitedBy(c.limitsOf(host))
			}
			return cl
		}
	}
	base := "https://api.github.com"
	if host != "github.com" {
		base = "https://" + host + "/api/v3"
	}
	if token == "" {
		token = c.tokens.For(ctx, host)
	}
	if token == "" {
		return API{T: noToken{host: host}}
	}
	var client *http.Client
	if c.HTTP != nil {
		client = c.HTTP()
	}
	return API{T: HTTP{Base: base, Token: token, Client: client, Limits: c.limitsOf(host)}}
}

// noToken is GitHub with no token found: every call is refused, as GitHub would refuse it.
type noToken struct{ host string }

func (n noToken) Do(context.Context, string, string, any, any) error {
	return &Error{Status: 401, Message: "no GitHub token found: set GITHUB_TOKEN, sign in with gh, or store a git credential for " + n.host}
}
