// Package runner starts agents inside their workspace's sandbox: it assembles
// the container, its mounts and environment, and returns the command the PTY
// runs.
package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// ProxyPort is where the egress sidecar listens.
const ProxyPort = 3128

// Name is the agent container for root.
func Name(root string) string { return "oriel-" + sandbox.Key(root) }

// ProxyName is the egress sidecar for root.
func ProxyName(root string) string { return Name(root) + "-egress" }

// NetworkName is root's internal network.
func NetworkName(root string) string { return Name(root) + "-net" }

// GoCacheDir is where every sandbox mounts the machine's shared Go module and
// build caches. Both are content-addressed and checked by go.sum, so
// workspaces can share them.
const GoCacheDir = "/cache/go"

func goCacheDir() string { return filepath.Join(sandbox.Base(), "cache", "go") }

// Options are the host facts a launch needs.
type Options struct {
	// ModDir is the oriel mod, mounted read-only for --plugin-dir.
	ModDir string
	// Environ is the environment the sandbox's allowlist picks from.
	Environ []string
	// Extra env the agent always gets (ORIEL_SPAWNED and friends).
	Extra map[string]string
	// Config is the sandbox's settings.
	Config sandbox.Config
	// ClaudeDefaults are settings the sandbox's Claude gets where the host's
	// settings leave them out.
	ClaudeDefaults map[string]any
}

// Launch readies root's sandbox for an agent in cwd running argv, and returns
// the command to run under the PTY in its place.
func Launch(ctx context.Context, root, cwd string, argv []string, o Options) ([]string, error) {
	rec, ok := sandbox.Load(root)
	if !ok || !rec.Sandboxed() {
		return nil, errors.New("this workspace is not sandboxed")
	}
	st := startSteps("sandbox launch " + filepath.Base(root))
	defer st.Done()
	cfg := o.Config
	id, err := identity()
	if err != nil {
		return nil, err
	}
	image, err := Image(ctx, root, cfg)
	if err != nil {
		return nil, err
	}
	st.Mark("image")
	markImageUsed(image)
	if err := writeUsers(ctx, root, image, id); err != nil {
		return nil, err
	}
	st.Mark("users")
	claudeBin, err := ClaudeBinary(ctx, cfg.ClaudeVersion)
	if err != nil {
		return nil, err
	}
	st.Mark("claude binary")
	orielBin, err := OrielBinary()
	if err != nil {
		return nil, err
	}
	host := sandbox.ClaudeSettings()
	if err := sandbox.PrepareClaude(root, cwd, host, o.ClaudeDefaults); err != nil {
		return nil, err
	}
	if !ownAuth(o.Environ, cfg) {
		if login, ok := sandbox.HostLogin(); ok {
			if err := sandbox.SyncLogin(root, login); err != nil {
				return nil, err
			}
		}
	}
	mounts, err := Mounts(root, cwd, id, claudeBin, orielBin, o.ModDir)
	if err != nil {
		return nil, err
	}
	bins, err := Provision(ctx, image, id, workspaceTools(root))
	if err != nil {
		return nil, err
	}
	st.Mark("toolchains")
	if len(bins) > 0 {
		mounts = append(mounts, container.Mount{Source: filepath.Join(toolchainDir(), "data"), Target: MiseDir + "/data", ReadOnly: true})
	}

	spec := container.Spec{
		Name:    Name(root),
		Image:   image,
		User:    fmt.Sprintf("%d:%d", id.UID, id.GID),
		Home:    id.Home,
		Workdir: root,
		Mounts:  mounts,
		Tmpfs:   []string{"/tmp:exec,mode=1777"},
		Hosts:   []string{sandbox.HostGateway + ":host-gateway"},
		Labels:  map[string]string{"oriel.root": root},

		Memory:    cfg.Memory,
		PidsLimit: cfg.PidsLimit,
	}
	env := agentEnv(root, id, cfg, o)
	if len(bins) > 0 {
		env["PATH"] = strings.Join(bins, ":") + ":" + env["PATH"]
	}
	// One container per agent run, gone when the agent exits; a leftover
	// from a run that was killed hard goes first.
	if err := container.Remove(ctx, spec.Name); err != nil {
		return nil, err
	}
	// Off Linux the sidecar also carries the session sockets, so it runs
	// even when nothing is filtered.
	if !cfg.NoEgressProxy || !sandbox.SocketsMount() {
		hostPorts := HostPorts(sandbox.EndpointEnv(o.Environ, cfg.Env, host))
		if err := ensureProxy(ctx, root, image, orielBin, id, hostPorts); err != nil {
			return nil, err
		}
		st.Mark("egress proxy")
	}
	if !cfg.NoEgressProxy {
		spec.Network = NetworkName(root)
		proxy := fmt.Sprintf("http://%s:%d", ProxyName(root), ProxyPort)
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env[k] = proxy
		}
		env["NO_PROXY"] = "localhost,127.0.0.1"
		env["no_proxy"] = env["NO_PROXY"]
	}
	if err := writeSSH(filepath.Join(sandbox.Dir(root), "home", ".ssh")); err != nil {
		return nil, err
	}
	if err := writeShims(filepath.Join(sandbox.Dir(root), "home")); err != nil {
		return nil, err
	}
	if err := container.PrepareMountpoints(spec.Mounts); err != nil {
		return nil, err
	}
	st.Mark("files")
	envFile := filepath.Join(sandbox.Dir(root), "agent.env")
	if err := os.WriteFile(envFile, []byte(sandbox.EnvFile(env)), 0o600); err != nil {
		return nil, err
	}
	spec.Workdir = cwd
	spec.Entrypoint, spec.Command = argv[0], argv[1:]
	return spec.InteractiveArgv(envFile), nil
}

// Stop removes root's containers and network. The workspace is untouched.
func Stop(ctx context.Context, root string) error {
	err := container.Remove(ctx, Name(root))
	if perr := container.Remove(ctx, ProxyName(root)); err == nil {
		err = perr
	}
	container.RemoveNetwork(ctx, NetworkName(root))
	container.RemoveVolume(ctx, RunVolumeName(root))
	return err
}

func agentEnv(root string, id sandbox.Identity, cfg sandbox.Config, o Options) map[string]string {
	env := sandbox.FilterEnv(o.Environ, cfg.Env)
	for k, v := range map[string]string{
		"HOME":              id.Home,
		"USER":              id.Name,
		"LOGNAME":           id.Name,
		"SHELL":             "/bin/bash",
		"PATH":              filepath.Join(id.Home, shimDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"LANG":              "C.UTF-8",
		"TERM":              "xterm-256color",
		"COLORTERM":         "truecolor",
		"CLAUDE_CONFIG_DIR": sandbox.ClaudeDir(root),
		"ORIEL_SOCKET":      sandbox.ContainerSocket,
		"SSH_AUTH_SOCK":     sandbox.ContainerRunDir + "/ssh-agent.sock",
		"ORIEL_TOKEN":       mustToken(root),
		"ORIEL_SANDBOX":     "1",
		"IS_SANDBOX":        "1",
		"MCP_TOOL_TIMEOUT":  strconv.Itoa(approvalMillis(cfg)),
		"GOMODCACHE":        GoCacheDir + "/mod",
		"GOCACHE":           GoCacheDir + "/build",
	} {
		env[k] = v
	}
	for k, v := range o.Extra {
		env[k] = v
	}
	if _, keyed := env["ANTHROPIC_API_KEY"]; !keyed {
		if _, authed := env["ANTHROPIC_AUTH_TOKEN"]; !authed {
			if tok := sandbox.ClaudeToken(); tok != "" {
				env["CLAUDE_CODE_OAUTH_TOKEN"] = tok
			}
		}
	}
	return env
}

// approvalMillis is how long an MCP call may wait on a person, plus slack.
func approvalMillis(cfg sandbox.Config) int {
	secs := cfg.ApprovalSecs
	if secs <= 0 {
		secs = 600
	}
	return (secs + 60) * 1000
}

func mustToken(root string) string {
	r, _ := sandbox.Load(root)
	return r.AgentToken
}

// Mounts is everything root's container sees of the host.
func Mounts(root, cwd string, id sandbox.Identity, claudeBin, orielBin, modDir string) ([]container.Mount, error) {
	dir := sandbox.Dir(root)
	home := filepath.Join(dir, "home")
	claudeDir := sandbox.ClaudeDir(root)
	for _, d := range []string{home, claudeDir, sandbox.RunDir(root), sandbox.TmpDir(root), goCacheDir()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	m := []container.Mount{
		{Source: home, Target: id.Home},
		{Source: root, Target: root},
		{Source: claudeDir, Target: claudeDir},
		{Source: filepath.Join(dir, "etc", "passwd"), Target: "/etc/passwd", ReadOnly: true},
		{Source: filepath.Join(dir, "etc", "group"), Target: "/etc/group", ReadOnly: true},
		{Source: claudeBin, Target: "/usr/local/bin/claude", ReadOnly: true},
		{Source: orielBin, Target: "/usr/local/bin/oriel", ReadOnly: true},
		{Source: sandbox.TmpDir(root), Target: fmt.Sprintf("/tmp/claude-%d", id.UID)},
		{Source: goCacheDir(), Target: GoCacheDir},
	}
	run, err := runMounts(context.Background(), root, id, false)
	if err != nil {
		return nil, err
	}
	m = append(m, run...)
	if modDir != "" {
		m = append(m, container.Mount{Source: modDir, Target: modDir, ReadOnly: true})
	}
	hostClaude := hostClaudeDir()
	for _, f := range sandbox.UserMounts(hostClaude) {
		m = append(m, container.Mount{Source: filepath.Join(hostClaude, f), Target: filepath.Join(claudeDir, f), ReadOnly: true})
	}
	transcripts := filepath.Join(hostClaude, "projects", projectDirName(cwd))
	if err := os.MkdirAll(transcripts, 0o700); err != nil {
		return nil, err
	}
	m = append(m, container.Mount{Source: transcripts, Target: filepath.Join(claudeDir, "projects", projectDirName(cwd))})
	// Pasted images: comments hand the agent their host paths.
	if state, ok := sandbox.StateDir(root); ok {
		att := filepath.Join(state, "attachments")
		if err := os.MkdirAll(att, 0o755); err != nil {
			return nil, err
		}
		m = append(m, container.Mount{Source: att, Target: att, ReadOnly: true})
	}
	if rec, _ := sandbox.Load(root); rec.Repo != "" {
		pm, err := gitMounts(rec.Repo, root)
		if err != nil {
			return nil, err
		}
		m = append(m, pm...)
	} else if ws, ok := sandbox.LoadManifest(root); ok {
		for _, p := range ws.Projects {
			pm, err := gitMounts(p.Repo, filepath.Join(root, p.Dir))
			if err != nil {
				return nil, fmt.Errorf("project %s: %w", p.Name, err)
			}
			m = append(m, pm...)
		}
	}
	return m, nil
}

// gitMounts give a worktree its repository's git directory, with the parts
// host git runs from (config, hooks, other worktrees, submodules, the main
// checkout's HEAD and index) read-only.
func gitMounts(repo, wt string) ([]container.Mount, error) {
	common := filepath.Join(repo, ".git")
	if out, err := sandbox.Git(repo, "rev-parse", "--path-format=absolute", "--git-common-dir"); err == nil {
		common = strings.TrimSpace(out)
	}
	admin, err := sandbox.WorktreeAdmin(filepath.Dir(common), wt)
	if err != nil {
		return nil, err
	}
	ro := func(p string) container.Mount { return container.Mount{Source: p, Target: p, ReadOnly: true} }
	m := []container.Mount{
		{Source: common, Target: common},
		ro(filepath.Join(common, "config")),
		ro(filepath.Join(common, "worktrees")),
		{Source: admin, Target: admin},
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
		m = append(m, ro(p))
	}
	for _, f := range readOnlyGitFiles {
		p := filepath.Join(common, f)
		fd, err := os.OpenFile(p, os.O_CREATE|os.O_RDONLY, 0o644)
		if err != nil {
			return nil, err
		}
		_ = fd.Close()
		m = append(m, ro(p))
	}
	// An empty index would stage every file's deletion, so a missing one
	// stays missing.
	if _, err := os.Stat(filepath.Join(common, "index")); err == nil {
		m = append(m, ro(filepath.Join(common, "index")))
	}
	return m, nil
}

// readOnlyGitDirs and readOnlyGitFiles are the shared git paths host git
// reads commands, filters or remotes from.
var (
	readOnlyGitDirs  = []string{"hooks", "modules", "info", "remotes", "branches"}
	readOnlyGitFiles = []string{"config.worktree"}
)

func hostClaudeDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// projectDirName is Claude Code's name for a cwd's transcript directory.
func projectDirName(dir string) string {
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

func identity() (sandbox.Identity, error) {
	u, err := user.Current()
	if err != nil {
		return sandbox.Identity{}, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	id := sandbox.Identity{Name: u.Username, UID: uid, GID: gid, Home: u.HomeDir}
	if g, err := user.LookupGroupId(u.Gid); err == nil {
		id.Group = g.Name
	}
	return id, nil
}

// writeUsers mirrors the host user into the image's passwd and group files.
func writeUsers(ctx context.Context, root, image string, id sandbox.Identity) error {
	etc := filepath.Join(sandbox.Dir(root), "etc")
	if err := os.MkdirAll(etc, 0o700); err != nil {
		return err
	}
	passwd, group, err := imageUsers(ctx, image)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(etc, "passwd"), []byte(sandbox.Passwd(passwd, id)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(etc, "group"), []byte(sandbox.Group(group, id)), 0o644)
}

// imageUsers is an image's own /etc/passwd and /etc/group. They are fixed for
// a given image, so they are read from a throwaway container once per image id
// and kept beside the sandboxes, rather than costing every launch a container.
func imageUsers(ctx context.Context, image string) (passwd, group string, err error) {
	imageID, _ := container.ImageID(ctx, image)
	cache := ""
	if imageID != "" {
		cache = filepath.Join(sandbox.Base(), "image-users", strings.NewReplacer(":", "-", "/", "-").Replace(imageID))
		if b, err := os.ReadFile(cache); err == nil {
			if p, g, ok := strings.Cut(string(b), "\x00"); ok {
				return p, g, nil
			}
		}
	}
	out, err := container.Output(ctx, image, "sh", "-c", `cat /etc/passwd; printf '\0'; cat /etc/group`)
	if err != nil {
		return "", "", err
	}
	p, g, ok := strings.Cut(out, "\x00")
	if !ok {
		return "", "", errors.New("the image's passwd and group files could not be read")
	}
	if cache != "" && os.MkdirAll(filepath.Dir(cache), 0o700) == nil {
		_ = os.WriteFile(cache, []byte(out), 0o600)
	}
	return p, g, nil
}

// OrielBinary is the oriel the container runs: ORIEL_SANDBOX_BIN when set, else
// this one on Linux, or a Linux build of it made on demand elsewhere.
func OrielBinary() (string, error) {
	if p := os.Getenv("ORIEL_SANDBOX_BIN"); p != "" {
		return p, nil
	}
	if runtime.GOOS == "linux" {
		exe, err := executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(exe)
	}
	return ensureLinuxOriel()
}

// hostClaude is the host's claude binary, symlinks resolved.
func hostClaude() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude is not installed on this machine")
	}
	return filepath.EvalSymlinks(p)
}
