package claude

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/core"
)

// Provider is Claude Code, run as an interactive claude in a PTY with Oriel's mod: the subscription's way in.
type Provider struct {
	Bin      string // claude; "" is claude on PATH
	OrielBin string // the binary the mod runs as `<OrielBin> hook <op>`; "" is this executable
}

var _ agent.Provider = Provider{}

// minVersion is the oldest Claude Code with the function hooks the mod uses.
const minVersion = "2.1.284"

// The PTY's size until a view says otherwise.
const cols, rows = 200, 50

func (Provider) ID() string { return "claude" }

func (p Provider) bin() string {
	if p.Bin != "" {
		return p.Bin
	}
	return "claude"
}

var semver = regexp.MustCompile(`\d+\.\d+\.\d+`)

func (p Provider) Detect(ctx context.Context) (agent.Install, error) {
	in := agent.Install{Bin: p.bin()}
	path, err := exec.LookPath(p.bin())
	if err != nil {
		in.Note = "Claude Code is not installed, or not on PATH."
		return in, nil
	}
	in.Bin = path
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	cmd.Dir = os.TempDir() // claude refuses to run from a directory that's gone
	out, err := cmd.Output()
	if err != nil {
		in.Note = "claude --version failed: " + err.Error()
		return in, nil
	}
	in.Version = semver.FindString(string(out))
	if in.Version == "" {
		in.Note = fmt.Sprintf("Could not read a version from claude --version: %q.", strings.TrimSpace(string(out)))
		return in, nil
	}
	in.LoggedIn, in.Account = login(nil)
	switch {
	case versionLess(in.Version, minVersion):
		in.Note = fmt.Sprintf("Claude Code %s is too old for Oriel, which needs %s or newer: run claude update.", in.Version, minVersion)
	case !in.LoggedIn:
		in.Note = "Not signed in: run claude and /login."
	case in.Account == "apiKey":
		in.Note = "Signed in with an API key: billed per token, not to a subscription."
	}
	return in, nil
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return false
}

// login reads the sign-in from Claude's files, without asking the network: the credentials file (Linux), else the
// account Claude records beside the trusted folders (macOS keeps the token in the Keychain), else an API key.
func login(env []string) (bool, string) {
	var cred struct {
		OAuth struct {
			Token        string `json:"accessToken"`
			Subscription string `json:"subscriptionType"`
		} `json:"claudeAiOauth"`
	}
	if b, err := os.ReadFile(filepath.Join(transcript.ConfigDir(env), ".credentials.json")); err == nil &&
		json.Unmarshal(b, &cred) == nil && cred.OAuth.Token != "" {
		return true, plan(cred.OAuth.Subscription)
	}
	var state struct {
		Account *struct {
			OrgType string `json:"organizationType"`
		} `json:"oauthAccount"`
	}
	if b, err := os.ReadFile(statePath(env)); err == nil && json.Unmarshal(b, &state) == nil && state.Account != nil {
		return true, plan(strings.TrimPrefix(state.Account.OrgType, "claude_"))
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true, "apiKey"
	}
	return false, ""
}

func plan(kind string) string {
	if kind == "" {
		return ""
	}
	return "Claude " + strings.ToUpper(kind[:1]) + kind[1:]
}

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

func (Provider) Models(context.Context) ([]agent.Model, error) {
	return []agent.Model{
		{ID: "opus", Name: "Opus", Efforts: efforts, Context: 200_000, Default: true},
		{ID: "opus[1m]", Name: "Opus (1M context)", Efforts: efforts, Context: 1_000_000},
		{ID: "sonnet", Name: "Sonnet", Efforts: efforts, Context: 200_000},
		{ID: "sonnet[1m]", Name: "Sonnet (1M context)", Efforts: efforts, Context: 1_000_000},
		{ID: "haiku", Name: "Haiku", Context: 200_000},
	}, nil
}

var (
	lastMu    sync.Mutex
	lastUsage core.ProviderUsage
)

// remember keeps the plan's windows a session last reported, for Usage.
func remember(u core.ProviderUsage) {
	lastMu.Lock()
	defer lastMu.Unlock()
	lastUsage = u
}

// Usage is the plan's windows as a session's mod last reported them ($.session.usage()); nothing until one has.
func (Provider) Usage(context.Context) (core.ProviderUsage, error) {
	lastMu.Lock()
	defer lastMu.Unlock()
	u := lastUsage
	u.Provider = "claude"
	if _, account := login(nil); account != "" && account != "apiKey" {
		u.Plan = account
	}
	return u, nil
}

// windowNames are what the windows $.session.usage() reports by kind are called.
var windowNames = map[string]string{
	"five_hour":        "Current session",
	"seven_day":        "Current week (all models)",
	"seven_day_opus":   "Current week (Opus)",
	"seven_day_sonnet": "Current week (Sonnet)",
}

func usageOf(rls []rateLimit) core.ProviderUsage {
	u := core.ProviderUsage{Provider: "claude", Sampled: time.Now()}
	for _, rl := range rls {
		name := windowNames[rl.Kind]
		if name == "" {
			name = strings.ReplaceAll(rl.Kind, "_", " ")
		}
		w := core.UsageWindow{Name: name, Used: rl.PercentUsed / 100}
		if t, err := time.Parse(time.RFC3339, rl.ResetsAt); err == nil {
			w.Resets = t
		}
		u.Windows = append(u.Windows, w)
	}
	return u
}

// userFiles are the parts of the host's Claude config a sandboxed agent reads.
var userFiles = []string{"CLAUDE.md", "skills", "agents", "commands"}

// SandboxCredentials are the host's Claude files a container needs, read-only. A Sandbox path is under
// $CLAUDE_CONFIG_DIR, the agent's config directory in the container. The sign-in itself is the sandbox's to hand
// over, stripped of its refresh token.
func (Provider) SandboxCredentials() []agent.Mount {
	dir := transcript.ConfigDir(nil)
	var out []agent.Mount
	for _, f := range userFiles {
		host := filepath.Join(dir, f)
		if _, err := os.Stat(host); err == nil {
			out = append(out, agent.Mount{Host: host, Sandbox: "$CLAUDE_CONFIG_DIR/" + f, ReadOnly: true})
		}
	}
	return out
}

func (p Provider) Start(ctx context.Context, s agent.StartSpec) (agent.Session, error) {
	return p.start(ctx, s, "")
}

func (p Provider) Resume(ctx context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	if id == "" {
		return nil, errors.New("claude: no session id to resume")
	}
	return p.start(ctx, s, id)
}

// permissionMode is the agent's own gate for a policy.
func permissionMode(s agent.StartSpec) (string, error) {
	switch s.Policy {
	case agent.PolicyAsk, "":
		return "default", nil
	case agent.PolicyLight:
		return "acceptEdits", nil
	case agent.PolicyBypass:
		if s.Sandbox == nil {
			return "", errors.New("claude: bypass is only for a sandboxed session: outside one, nothing would gate the agent")
		}
		return "bypassPermissions", nil
	}
	return "", fmt.Errorf("claude: unknown policy %q", s.Policy)
}

// args is claude's command line. A fresh session starts on its pack's skill; a resumed one has it already.
func args(s agent.StartSpec, modDir, id, resume string) ([]string, error) {
	mode, err := permissionMode(s)
	if err != nil {
		return nil, err
	}
	a := []string{"--plugin-dir", modDir, "--permission-mode", mode}
	if s.Model != "" {
		a = append(a, "--model", s.Model)
	}
	if s.Effort != "" {
		a = append(a, "--effort", s.Effort)
	}
	if s.Sandbox != nil && s.Sandbox.GatewayMCP != "" {
		a = append(a, "--mcp-config", s.Sandbox.GatewayMCP, "--strict-mcp-config")
	}
	if resume != "" {
		return append(a, "--resume", resume), nil
	}
	a = append(a, "--session-id", id)
	first := strings.TrimSpace(s.Prompt)
	if s.Pack.Skill != "" {
		first = strings.TrimSpace("/oriel:" + s.Pack.Skill + " " + first)
	}
	if first != "" {
		if strings.HasPrefix(first, "-") {
			a = append(a, "--")
		}
		a = append(a, first)
	}
	return a, nil
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (p Provider) start(ctx context.Context, spec agent.StartSpec, resume string) (_ agent.Session, err error) {
	if spec.Cwd == "" {
		return nil, errors.New("claude: no directory to start in")
	}
	id := resume
	if id == "" {
		id = newUUID()
	}
	modDir := filepath.Join(spec.StateDir, "claude-mod")
	temp := ""
	if spec.StateDir == "" {
		if temp, err = os.MkdirTemp("", "oriel-claude-"); err != nil {
			return nil, err
		}
		modDir = filepath.Join(temp, "mod")
	}
	defer func() {
		if err != nil && temp != "" {
			_ = os.RemoveAll(temp)
		}
	}()
	argv, err := args(spec, modDir, id, resume)
	if err != nil {
		return nil, err
	}
	bin := p.bin()
	exe := spec.Exec
	if exe == nil {
		exe = hostexec.Exec{}
		if bin, err = exec.LookPath(bin); err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
	}
	orielBin := p.OrielBin
	if orielBin == "" {
		if orielBin, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	if err := writeMod(modDir, spec.Pack); err != nil {
		return nil, err
	}
	// Inside a sandbox its own config is prepared for it; on the host, the folder is trusted first, or claude
	// stops at a dialog nobody is there to answer.
	if spec.Sandbox == nil {
		if err := trust(spec.Env, spec.Cwd); err != nil {
			return nil, fmt.Errorf("claude: trusting %s: %w", spec.Cwd, err)
		}
		_ = ensureDefaults(spec.Env)
	}
	dir, err := sockDir()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("unix", sockPath(dir))
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.Command(bin, argv...)
	cmd.Dir = spec.Cwd
	cmd.Env = append(os.Environ(),
		"ORIEL_SPAWNED=1",
		"CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1",
		agent.HookSocketEnv+"="+sockPath(dir),
		"ORIEL_BIN="+orielBin,
	)
	if spec.Sandbox != nil {
		cmd.Env = append(cmd.Env, "ENABLE_CLAUDEAI_MCP_SERVERS=false")
	}
	cmd.Env = append(cmd.Env, spec.Env...)

	sctx, cancel := context.WithCancel(ctx)
	pty, err := exe.StartPTY(sctx, cmd, cols, rows)
	if err != nil {
		cancel()
		_ = ln.Close()
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("claude: %w", err)
	}
	// In a sandbox the log is written through a mount of the host's projects folder, so it's read where the host
	// keeps it.
	config := transcript.ConfigDir(spec.Env)
	if spec.Sandbox != nil {
		config = transcript.ConfigDir(nil)
	}
	s := &session{
		spec:   spec,
		ctx:    sctx,
		cancel: cancel,
		pty:    pty,
		term:   newTerminal(pty),
		ln:     ln,
		dir:    dir,
		temp:   temp,
		tail:   newTailer(config, spec.Cwd, id, resume != ""),
		events: newQueue(),
		done:   make(chan struct{}),
		id:     id,
		wake:   make(chan struct{}, 1),
		asks:   map[string]chan agent.Decision{},
		forks:  map[string]chan forkAnswerIn{},
	}
	if resume != "" && strings.TrimSpace(spec.Prompt) != "" {
		s.cmds = append(s.cmds, waitOut{Kind: "submit", Text: strings.TrimSpace(spec.Prompt)})
	}
	go s.term.drain()
	go s.serve()
	go s.tail.follow(s.done, s.push)
	go s.run()
	return s, nil
}
