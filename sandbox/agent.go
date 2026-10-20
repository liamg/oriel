package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/sandbox/egress"
)

// What every backend gives its agent, wherever it puts it.

// Bind is a host path the agent sees, at Agent. In order: a later bind under
// an earlier one overrides it.
type Bind struct {
	Host     string
	Agent    string
	ReadOnly bool
}

// GoCache is the machine's Go module and build cache, shared by every
// sandbox: both are content-addressed and checked by go.sum.
func GoCache() string { return filepath.Join(Base(), "cache", "go") }

// HomeDir is root's sandbox home: the agent's $HOME.
func HomeDir(root string) string { return filepath.Join(Dir(root), "home") }

// Binds are the host paths every backend gives an agent in cwd: the
// workspace, its Claude config, transcripts and attachments, the user's
// read-only Claude files, the mod, and each project's git directory. The Go
// cache, home, temp and run dirs are the backend's to place.
func Binds(root, cwd, modDir string) ([]Bind, error) {
	claudeDir := ClaudeDir(root)
	for _, d := range []string{HomeDir(root), claudeDir, RunDir(root), TmpDir(root), GoCache()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	same := func(p string, ro bool) Bind { return Bind{Host: p, Agent: p, ReadOnly: ro} }
	b := []Bind{same(root, false), same(claudeDir, false)}
	if modDir != "" {
		b = append(b, same(modDir, true))
	}
	hostClaude := HostClaudeDir()
	for _, f := range UserMounts(hostClaude) {
		b = append(b, Bind{Host: filepath.Join(hostClaude, f), Agent: filepath.Join(claudeDir, f), ReadOnly: true})
	}
	transcripts := filepath.Join(hostClaude, "projects", ProjectDirName(cwd))
	if err := os.MkdirAll(transcripts, 0o700); err != nil {
		return nil, err
	}
	b = append(b, Bind{Host: transcripts, Agent: filepath.Join(claudeDir, "projects", ProjectDirName(cwd))})
	// Pasted images: comments hand the agent their host paths.
	if state, ok := StateDir(root); ok {
		att := filepath.Join(state, "attachments")
		if err := os.MkdirAll(att, 0o755); err != nil {
			return nil, err
		}
		b = append(b, same(att, true))
	}
	if rec, _ := Load(root); rec.Repo != "" {
		gb, err := GitBinds(rec.Repo, root)
		if err != nil {
			return nil, err
		}
		b = append(b, gb...)
	} else if ws, ok := LoadManifest(root); ok {
		for _, p := range ws.Projects {
			gb, err := GitBinds(p.Repo, filepath.Join(root, p.Dir))
			if err != nil {
				return nil, fmt.Errorf("project %s: %w", p.Name, err)
			}
			b = append(b, gb...)
		}
		for _, dir := range ws.Reads {
			if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
				b = append(b, same(dir, true))
			}
		}
	}
	return b, nil
}

// GitBinds give a worktree its repository's git directory, with the parts
// host git runs from (config, hooks, other worktrees, submodules, the main
// checkout's HEAD and index) read-only.
func GitBinds(repo, wt string) ([]Bind, error) {
	common := filepath.Join(repo, ".git")
	if out, err := Git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		common = strings.TrimSpace(out)
	}
	admin, err := WorktreeAdmin(filepath.Dir(common), wt)
	if err != nil {
		return nil, err
	}
	ro := func(p string) Bind { return Bind{Host: p, Agent: p, ReadOnly: true} }
	b := []Bind{
		{Host: common, Agent: common},
		ro(filepath.Join(common, "config")),
		ro(filepath.Join(common, "worktrees")),
		{Host: admin, Agent: admin},
		ro(filepath.Join(admin, "gitdir")),
		ro(filepath.Join(admin, "commondir")),
		ro(filepath.Join(common, "HEAD")),
	}
	// Missing, the agent could create them for host git to find.
	for _, d := range readOnlyGitDirs {
		p := filepath.Join(common, d)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return nil, err
		}
		b = append(b, ro(p))
	}
	for _, f := range readOnlyGitFiles {
		p := filepath.Join(common, f)
		fd, err := os.OpenFile(p, os.O_CREATE|os.O_RDONLY, 0o644)
		if err != nil {
			return nil, err
		}
		_ = fd.Close()
		b = append(b, ro(p))
	}
	// An empty index would stage every file's deletion, so a missing one
	// stays missing.
	if _, err := os.Stat(filepath.Join(common, "index")); err == nil {
		b = append(b, ro(filepath.Join(common, "index")))
	}
	return b, nil
}

// readOnlyGitDirs and readOnlyGitFiles are the shared git paths host git
// reads commands, filters or remotes from.
var (
	readOnlyGitDirs  = []string{"hooks", "modules", "info", "remotes", "branches"}
	readOnlyGitFiles = []string{"config.worktree"}
)

// HostClaudeDir is the host's ~/.claude.
func HostClaudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// ProjectDirName is Claude Code's name for a cwd's transcript directory.
func ProjectDirName(dir string) string {
	var b strings.Builder
	for _, r := range dir {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// CurrentIdentity is the user oriel runs as.
func CurrentIdentity() (Identity, error) {
	u, err := user.Current()
	if err != nil {
		return Identity{}, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	id := Identity{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir}
	if g, err := user.LookupGroupId(u.Gid); err == nil {
		id.Group = g.Name
	}
	return id, nil
}

// Prepare writes what an agent in cwd reads before it starts: its Claude
// settings and sign-in, and its home's ssh config, shims and git config.
func Prepare(root, cwd string, o LaunchOptions) error {
	if err := PrepareClaude(root, cwd, ClaudeSettings(), o.ClaudeDefaults); err != nil {
		return err
	}
	if !OwnAuth(o.Environ, o.Config) {
		if login, ok := HostLogin(); ok {
			if err := SyncLogin(root, login); err != nil {
				return err
			}
		}
	}
	home := HomeDir(root)
	if err := writeSSH(filepath.Join(home, ".ssh")); err != nil {
		return err
	}
	return writeShims(home)
}

// AgentEnv is the environment every backend gives its agent: the host's
// allowlisted variables, the session's, o.Extra and a Claude token, over
// at, the backend's own places (HOME, PATH, the sockets). With rewrite,
// localhost endpoints point at HostGateway.
func AgentEnv(root string, o LaunchOptions, rewrite bool, at map[string]string) map[string]string {
	env := HostEnv(o.Environ, o.Config.Env)
	if rewrite {
		env = FilterEnv(o.Environ, o.Config.Env)
	}
	rec, _ := Load(root)
	for k, v := range map[string]string{
		"SHELL":             "/bin/bash",
		"LANG":              "C.UTF-8",
		"TERM":              "xterm-256color",
		"COLORTERM":         "truecolor",
		"CLAUDE_CONFIG_DIR": ClaudeDir(root),
		"ORIEL_TOKEN":       rec.AgentToken,
		"ORIEL_SANDBOX":     "1",
		"IS_SANDBOX":        "1",
		"MCP_TOOL_TIMEOUT":  strconv.Itoa(approvalMillis(o.Config)),
	} {
		env[k] = v
	}
	for k, v := range at {
		env[k] = v
	}
	for k, v := range o.Extra {
		env[k] = v
	}
	if _, keyed := env["ANTHROPIC_API_KEY"]; !keyed {
		if _, authed := env["ANTHROPIC_AUTH_TOKEN"]; !authed {
			if tok := ClaudeToken(); tok != "" {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
			}
		}
	}
	return env
}

// approvalMillis is how long an MCP call may wait on a person, plus slack.
func approvalMillis(cfg Config) int {
	secs := cfg.ApprovalSecs
	if secs <= 0 {
		secs = 600
	}
	return (secs + 60) * 1000
}

// HostPorts are the host ports the sandbox's environment points at.
func HostPorts(env map[string]string) []int {
	var out []int
	for _, r := range egress.EnvRules(env) {
		if r.Host == HostGateway && !slices.Contains(out, r.Port) {
			out = append(out, r.Port)
		}
	}
	slices.Sort(out)
	return out
}

// OwnAuth reports credentials a sandbox is handed outright, which come before
// the host's login: a setup token, an API key or a key helper.
func OwnAuth(environ []string, cfg Config) bool {
	if ClaudeToken() != "" {
		return true
	}
	env := HostEnv(environ, cfg.Env)
	if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		return true
	}
	helper, _ := ClaudeSettings()["apiKeyHelper"].(string)
	return strings.TrimSpace(helper) != ""
}

// LoginProblems are what stops a sandboxed agent signing in to Claude.
func LoginProblems(cfg Config) []Problem {
	if OwnAuth(os.Environ(), cfg) {
		if age, ok := TokenAge(); ok && age > 330*24*time.Hour {
			return []Problem{{Code: "claude-token-expiring",
				Message: "The sandbox's Claude token is nearly a year old and will expire soon",
				Fix:     "Run `claude setup-token` again and save the new token, or clear it with `oriel sandbox token --clear` to use this machine's own login."}}
		}
		return nil
	}
	login, ok := HostLogin()
	if !ok {
		return []Problem{{Code: "claude-token-missing", Blocking: true,
			Message: "Sandboxed agents have no way to sign in to Claude",
			Fix:     "Sign in with `claude` on this machine; sandboxes share that login."}}
	}
	SyncLogins(login)
	if time.Until(login.Expiry()) <= 0 {
		return []Problem{{Code: "claude-login-stale",
			Message: "This machine's Claude login needs refreshing before sandboxed agents can use it",
			Fix:     "Run `claude` on this machine once; sandboxes pick the fresh login up within a minute."}}
	}
	return nil
}

const sshMarker = "# written by oriel"

// writeSSH gives the sandbox's home the host's known_hosts, which is not a
// secret, and an ssh config that goes out through `oriel ssh-proxy`. A config
// the agent wrote itself is left alone.
func writeSSH(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "known_hosts"), b, 0o600); err != nil {
			return err
		}
	}
	cfg := filepath.Join(dir, "config")
	if cur, err := os.ReadFile(cfg); err == nil && !strings.HasPrefix(string(cur), sshMarker) {
		return nil
	}
	body := sshMarker + "\nHost *\n  ProxyCommand oriel ssh-proxy %h %p\n"
	return os.WriteFile(cfg, []byte(body), 0o600)
}

// ShimDir, in the sandbox's home, holds commands that stand in for ones with
// no credentials in a sandbox, first on the agent's PATH.
const ShimDir = ".oriel-bin"

const ghShim = `#!/bin/sh
echo "oriel sandbox: gh has no credentials here. Use the oriel-gw gateway tools instead: oriel__pr_create to open the PR, oriel__ci for checks and logs, oriel__push to push, oriel__gh_* for tags, workflow runs, compares and repo variables, or github__* when the GitHub server is installed." >&2
exit 1
`

// writeShims gives the sandbox's home a gh that says what to use instead,
// and a git config with the host's identity and a credential helper that
// explains why there are no credentials. Files the agent wrote are kept.
func writeShims(home string) error {
	bin := filepath.Join(home, ShimDir)
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghShim), 0o700); err != nil {
		return err
	}
	cfg := filepath.Join(home, ".gitconfig")
	if cur, err := os.ReadFile(cfg); err == nil && !strings.HasPrefix(string(cur), sshMarker) {
		return nil
	}
	body := sshMarker + "\n"
	if name, email := hostGitIdentity(); name != "" || email != "" {
		body += "[user]\n"
		if name != "" {
			body += "\tname = " + name + "\n"
		}
		if email != "" {
			body += "\temail = " + email + "\n"
		}
	}
	body += "[credential]\n\thelper = !oriel git-credential\n"
	return os.WriteFile(cfg, []byte(body), 0o600)
}

func hostGitIdentity() (name, email string) {
	get := func(key string) string {
		out, err := exec.Command("git", "config", "--global", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
}
