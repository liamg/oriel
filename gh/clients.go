package gh

import (
	"cmp"
	"context"
	"net/http"
	"strings"
)

// Clients make the GitHub client for a checkout or a server: Client's when it gives one (a fake, in tests), else
// GitHub's API with the token given, or the one found for the server (TokenFor). gh needn't be installed.
type Clients struct {
	// Client is the GitHub client for a checkout, given its git guard's env; nil, or nil from it, for GitHub's API.
	Client func(dir string, env []string) Client
	// HTTP is what GitHub's API is asked through; http.DefaultClient if nil, or it gives nil.
	HTTP   func() *http.Client
	tokens Tokens
}

// On is the client for the checkout at dir with env, on a GitHub Enterprise server's hostname when given (github.com
// if ""), with token, else the one found for that server.
func (c *Clients) On(dir string, env []string, hostname, token string) Client {
	if c.Client != nil {
		if cl := c.Client(dir, env); cl != nil {
			return cl
		}
	}
	host := strings.ToLower(cmp.Or(hostname, "github.com"))
	base := "https://api.github.com"
	if host != "github.com" {
		base = "https://" + host + "/api/v3"
	}
	if token == "" {
		token = c.tokens.For(context.Background(), host)
	}
	if token == "" {
		return API{T: noToken{host: host}}
	}
	var client *http.Client
	if c.HTTP != nil {
		client = c.HTTP()
	}
	return API{T: HTTP{Base: base, Token: token, Client: client}}
}

// noToken is GitHub with no token found: every call is refused, as GitHub would refuse it.
type noToken struct{ host string }

func (n noToken) Do(context.Context, string, string, any, any) error {
	return &Error{Status: 401, Message: "no GitHub token found: set GITHUB_TOKEN, sign in with gh, or store a git credential for " + n.host}
}
