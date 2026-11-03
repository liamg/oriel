package claudebox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
)

// passEnv is the host environment Claude Code's agent inherits by default: its own settings, the model endpoint,
// never credentials for anything else.
var passEnv = []string{
	"ANTHROPIC_*",
	"DISABLE_*",
	"CLAUDE_CODE_*",
	"MAX_THINKING_TOKENS",
	"MAX_MCP_OUTPUT_TOKENS",
	"MCP_TIMEOUT",
	"API_TIMEOUT_MS",
	"BASH_DEFAULT_TIMEOUT_MS",
	"BASH_MAX_TIMEOUT_MS",
	"BASH_MAX_OUTPUT_LENGTH",
}

// skipEnv is what a Claude Code session sets for its own processes, which the defaults leave out: Oriel started
// from inside one isn't that session's child. The sandbox's sign-in is Oriel's to hand over.
var skipEnv = []string{
	"CLAUDE_CODE_ENTRYPOINT",
	"CLAUDE_CODE_EXECPATH",
	"CLAUDE_CODE_SSE_PORT",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CODE_SESSION_*",
	"CLAUDE_CODE_MESSAGING_*",
	"CLAUDE_CODE_OAUTH_TOKEN",
}

// urlEnv are the model endpoints, whose localhost means the host.
var urlEnv = []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_BEDROCK_BASE_URL", "ANTHROPIC_VERTEX_BASE_URL"}

// hosts are what Claude Code reaches, allowed by default.
var hosts = []string{
	"api.anthropic.com",
	"claude.ai",
	"*.claude.ai",
	"statsig.anthropic.com",
	// Claude Code refreshes its login here; blocked, every request fails once the token expires.
	"platform.claude.com",
	"console.anthropic.com",
	"mcp-proxy.anthropic.com",
}

// Needs are what Claude Code needs at at: its binary (pinned to a version, or the host's), its config and sign-in
// written to at.Dir, the user's own Claude files read-only, its transcripts written back where the host keeps them,
// and modDir (Oriel's mod).
func (x *Box) Needs(ctx context.Context, at agent.SandboxPlace, modDir, pinned string) (agent.SandboxNeeds, error) {
	n := agent.SandboxNeeds{PassEnv: passEnv, SkipEnv: skipEnv, URLEnv: urlEnv, Hosts: hosts,
		Env: map[string]string{"CLAUDE_CONFIG_DIR": at.Dir}}
	if n.Problems = x.Problems(at, pinned); blocking(n.Problems) {
		return n, nil
	}
	bin, err := x.Binary(ctx, at, pinned)
	if err != nil {
		return n, err
	}
	n.Bins = map[string]string{"claude": bin}
	settings := Settings()
	seen := x.SeenFor(bin, at.Shared || runtime.GOOS == at.OS && runtime.GOARCH == at.Arch)
	if err := Prepare(at.Root, at.Cwd, settings, nil, seen); err != nil {
		return n, err
	}
	if !ownAuth(at, settings) {
		if login, ok := x.HostLogin(); ok {
			if err := SyncLogin(at.Root, login); err != nil {
				return n, err
			}
		}
	}
	if tok := Token(); tok != "" && at.Env("ANTHROPIC_API_KEY") == "" && at.Env("ANTHROPIC_AUTH_TOKEN") == "" {
		n.Env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
	}
	if env, ok := settings["env"].(map[string]any); ok {
		for _, k := range urlEnv {
			if v, ok := env[k].(string); ok {
				n.URLs = append(n.URLs, v)
			}
		}
	}
	hostDir := HostDir()
	for _, f := range UserMounts(hostDir) {
		n.Mounts = append(n.Mounts, agent.Mount{Host: filepath.Join(hostDir, f), Sandbox: filepath.Join(at.Dir, f), ReadOnly: true})
	}
	transcripts := filepath.Join(hostDir, "projects", sandbox.ProjectDirName(at.Cwd))
	if err := os.MkdirAll(transcripts, 0o700); err != nil {
		return n, err
	}
	n.Mounts = append(n.Mounts, agent.Mount{Host: transcripts, Sandbox: filepath.Join(at.Dir, "projects", sandbox.ProjectDirName(at.Cwd))})
	if modDir != "" {
		n.Mounts = append(n.Mounts, agent.Mount{Host: modDir, ReadOnly: true})
	}
	if !at.Shared && at.Tmp != "" {
		n.Mounts = append(n.Mounts, agent.Mount{Host: at.Tmp, Sandbox: fmt.Sprintf("/tmp/claude-%d", at.UID)})
	}
	return n, nil
}

// HostDir is the host's ~/.claude.
func HostDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

func blocking(ps []agent.SandboxProblem) bool {
	for _, p := range ps {
		if p.Blocking {
			return true
		}
	}
	return false
}

// ownAuth reports credentials a sandbox is handed outright, which come before the host's login: a setup token, an
// API key or a key helper.
func ownAuth(at agent.SandboxPlace, settings map[string]any) bool {
	if Token() != "" || at.Env("ANTHROPIC_API_KEY") != "" || at.Env("ANTHROPIC_AUTH_TOKEN") != "" {
		return true
	}
	helper, _ := settings["apiKeyHelper"].(string)
	return strings.TrimSpace(helper) != ""
}

// Problems are what stops a sandboxed Claude at at: not installed where the OS's sandbox runs the host's, or no
// way to sign in; and a pinned version that won't apply.
func (x *Box) Problems(at agent.SandboxPlace, pinned string) []agent.SandboxProblem {
	var out []agent.SandboxProblem
	if at.Shared {
		if _, err := hostClaude(); err != nil {
			out = append(out, agent.SandboxProblem{Code: "claude-missing", Blocking: true,
				Message: "claude is not installed on this machine", Fix: "Install Claude Code; this sandbox runs the host's."})
		}
		if pinned != "" {
			out = append(out, agent.SandboxProblem{Code: "settings-unused", Message: "sandbox.claude_version only applies to container sandboxes",
				Fix: "Clear it, or set sandbox.backend to docker."})
		}
	}
	if ownAuth(at, Settings()) {
		if age, ok := TokenAge(); ok && age > 330*24*time.Hour {
			out = append(out, agent.SandboxProblem{Code: "claude-token-expiring",
				Message: "The sandbox's Claude token is nearly a year old and will expire soon",
				Fix:     "Run `claude setup-token` again and save the new token, or clear it to use this machine's own login."})
		}
		return out
	}
	login, ok := x.HostLogin()
	if !ok {
		return append(out, agent.SandboxProblem{Code: "claude-token-missing", Blocking: true,
			Message: "Claude isn't signed in on this machine, and sandboxes share its sign-in",
			Fix:     "Run `claude auth login` on this machine; sandboxes share that login.", SignIn: []string{"claude", "auth", "login"}})
	}
	SyncLogins(login)
	if time.Until(login.Expiry()) <= 0 {
		out = append(out, agent.SandboxProblem{Code: "claude-login-stale",
			Message: "This machine's Claude login needs refreshing before sandboxed agents can use it",
			Fix:     "Run `claude` on this machine once; sandboxes pick the fresh login up within a minute."})
	}
	return out
}
