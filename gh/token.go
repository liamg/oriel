package gh

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Token is the user's github.com token, for private repos' releases and the plugin registries: TokenFor github.com.
func Token(ctx context.Context) string { return TokenFor(ctx, "") }

// TokenFor is the user's token for a GitHub server (github.com if host is ""), found in order: GH_TOKEN or
// GITHUB_TOKEN (GH_ENTERPRISE_TOKEN or GITHUB_ENTERPRISE_TOKEN for an Enterprise server), gh's login when gh is
// installed, then git's credential helper for the host. "" without one: public repos are read without it.
func TokenFor(ctx context.Context, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	enterprise := host != "" && host != "github.com"
	vars := []string{"GH_TOKEN", "GITHUB_TOKEN"}
	if enterprise {
		vars = []string{"GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}
	}
	for _, name := range vars {
		if tok := strings.TrimSpace(os.Getenv(name)); tok != "" {
			return tok
		}
	}
	if tok := ghLogin(ctx, host); tok != "" {
		return tok
	}
	return gitCredential(ctx, host)
}

// ghLogin is gh's token for host, when gh is installed and signed in there.
func ghLogin(ctx context.Context, host string) string {
	if _, err := exec.LookPath("gh"); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	args := []string{"auth", "token"}
	if host != "" {
		args = append(args, "--hostname", host)
	}
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// gitCredential is the password git's credential helper keeps for https://host, asked without prompting.
func gitCredential(ctx context.Context, host string) string {
	if host == "" {
		host = "github.com"
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-c", "core.askPass=", "credential", "fill")
	cmd.Stdin = strings.NewReader("protocol=https\nhost=" + host + "\n\n")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=", "GCM_INTERACTIVE=never")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "password="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// Tokens keeps the token TokenFor found for each host, so asking again doesn't run gh or git each time. A host with
// none is asked again after a minute.
type Tokens struct {
	mu    sync.Mutex
	found map[string]foundToken
	Find  func(ctx context.Context, host string) string // TokenFor if nil
}

type foundToken struct {
	tok string
	at  time.Time
}

// For is host's token, found once.
func (t *Tokens) For(ctx context.Context, host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	t.mu.Lock()
	defer t.mu.Unlock()
	if f, ok := t.found[host]; ok && (f.tok != "" || time.Since(f.at) < time.Minute) {
		return f.tok
	}
	find := t.Find
	if find == nil {
		find = TokenFor
	}
	tok := find(ctx, host)
	if t.found == nil {
		t.found = map[string]foundToken{}
	}
	t.found[host] = foundToken{tok: tok, at: time.Now()}
	return tok
}
