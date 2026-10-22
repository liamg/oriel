package codex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/jsonrpc"
)

// loginTimeout is how long a browser sign-in may take before it's given up.
const loginTimeout = 15 * time.Minute

// loginMark tells one sign-in in $CODEX_HOME/auth.json from the next (agent.Install.Login): a hash of its token, or
// of its API key; "" when the sign-in isn't kept there (the OS's keyring).
func loginMark(dir string) string {
	var a struct {
		APIKey string `json:"OPENAI_API_KEY"`
		Tokens *struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil || json.Unmarshal(b, &a) != nil {
		return ""
	}
	secret := a.APIKey
	if a.Tokens != nil && a.Tokens.Access != "" {
		secret = a.Tokens.Access
	}
	if secret == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}

var _ agent.LoginStarter = (*Provider)(nil)

// StartLogin signs in with ChatGPT in the browser: an app-server of its own runs account/login/start and waits for
// account/login/completed, then goes.
func (p *Provider) StartLogin(ctx context.Context) (agent.Login, error) {
	type completed struct {
		LoginID string  `json:"loginId"`
		Success bool    `json:"success"`
		Error   *string `json:"error"`
	}
	ends := make(chan completed, 4)
	life, cancel := context.WithTimeout(context.Background(), loginTimeout)
	c, err := p.serve(life, jsonrpc.Handlers{Notify: func(method string, params json.RawMessage) {
		if method != "account/login/completed" {
			return
		}
		var n completed
		if json.Unmarshal(params, &n) == nil {
			select {
			case ends <- n:
			default:
			}
		}
	}})
	if err != nil {
		cancel()
		return agent.Login{}, fmt.Errorf("codex: %w", err)
	}
	var r struct {
		Type    string `json:"type"`
		LoginID string `json:"loginId"`
		AuthURL string `json:"authUrl"`
	}
	if err := c.rpc.Call(ctx, "account/login/start", map[string]any{"type": "chatgpt"}, &r); err != nil {
		c.close()
		cancel()
		return agent.Login{}, fmt.Errorf("codex account/login/start: %w", err)
	}
	if r.AuthURL == "" {
		c.close()
		cancel()
		return agent.Login{}, errors.New("codex account/login/start: no URL to sign in at")
	}
	done := make(chan error, 1)
	go func() {
		defer cancel()
		defer c.close()
		for {
			select {
			case n := <-ends:
				if n.LoginID != "" && n.LoginID != r.LoginID {
					continue
				}
				switch {
				case n.Success:
					done <- nil
				case n.Error != nil && *n.Error != "":
					done <- errors.New("codex: " + *n.Error)
				default:
					done <- errors.New("codex: the sign-in didn't complete")
				}
				return
			case <-life.Done():
				done <- errors.New("codex: the sign-in timed out")
				return
			}
		}
	}()
	return agent.Login{URL: r.AuthURL, Done: done}, nil
}
