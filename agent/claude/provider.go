package claude

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
	ProbeDir string // a folder of Oriel's that the /usage probe runs claude in; "" is one in the temp dir
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
	in.LoggedIn, in.Account, in.Login = login(nil)
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

// login reads the sign-in from Claude's files, without asking the network (subscription), else an API key. mark
// tells one token in the file from the next (agent.Install.Login).
func login(env []string) (ok bool, account, mark string) {
	if kind, _, mark, ok := subscription(env); ok {
		return true, plan(kind), mark
	}
	if os.Getenv("ANTHROPIC_API_KEY") != "" {
		return true, "apiKey", ""
	}
	return false, "", ""
}

// subscription is the plan Claude's files record and whether they record a sign-in: the credentials file's
// subscriptionType and rateLimitTier (Linux), with a mark of its token, else oauthAccount's organizationType and
// organizationRateLimitTier in the state file (macOS keeps the token in the Keychain, so there's no mark).
func subscription(env []string) (kind, limit, mark string, ok bool) {
	var cred struct {
		OAuth struct {
			Token        string `json:"accessToken"`
			Subscription string `json:"subscriptionType"`
			Tier         string `json:"rateLimitTier"`
		} `json:"claudeAiOauth"`
	}
	if b, err := os.ReadFile(filepath.Join(transcript.ConfigDir(env), ".credentials.json")); err == nil &&
		json.Unmarshal(b, &cred) == nil && cred.OAuth.Token != "" {
		sum := sha256.Sum256([]byte(cred.OAuth.Token))
		return cred.OAuth.Subscription, cred.OAuth.Tier, hex.EncodeToString(sum[:8]), true
	}
	var state struct {
		Account *struct {
			OrgType string `json:"organizationType"`
			Tier    string `json:"organizationRateLimitTier"`
		} `json:"oauthAccount"`
	}
	if b, err := os.ReadFile(statePath(env)); err == nil && json.Unmarshal(b, &state) == nil && state.Account != nil {
		return strings.TrimPrefix(state.Account.OrgType, "claude_"), state.Account.Tier, "", true
	}
	return "", "", "", false
}

// planOf is the plan Claude is signed in to ("Claude Max") and its tier, for its chip ("Max 20×"); "" for what
// isn't known.
func planOf(env []string) (name, tier string) {
	kind, limit, _, _ := subscription(env)
	return plan(kind), tierOf(kind, limit)
}

// tiers are the subscriptions Claude Code names, as a chip reads them.
var tiers = map[string]string{"max": "Max", "pro": "Pro", "team": "Team", "enterprise": "Enterprise"}

// maxTier is a Max plan's rate limit tier: default_claude_max_20x is twenty times Pro's.
var maxTier = regexp.MustCompile(`^default_claude_max_(\d+)x$`)

func tierOf(kind, limit string) string {
	name, ok := tiers[kind]
	if !ok {
		return ""
	}
	if m := maxTier.FindStringSubmatch(limit); kind == "max" && m != nil {
		return name + " " + m[1] + "×"
	}
	return name
}

// account is who Claude is signed in as, from its state file's oauthAccount: an identity that tells a company
// subscription from a personal one (the organisation, then the account), and the email or organisation to show.
func account(env []string) (id, name string) {
	var state struct {
		Account *struct {
			Email   string `json:"emailAddress"`
			UUID    string `json:"accountUuid"`
			OrgUUID string `json:"organizationUuid"`
			OrgName string `json:"organizationName"`
		} `json:"oauthAccount"`
	}
	b, err := os.ReadFile(statePath(env))
	if err != nil || json.Unmarshal(b, &state) != nil || state.Account == nil {
		return "", ""
	}
	a := state.Account
	switch {
	case a.OrgUUID != "" || a.UUID != "":
		id = strings.Trim(a.UUID+"/"+a.OrgUUID, "/")
	default:
		id = a.Email
	}
	return id, cmpOr(a.Email, a.OrgName)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
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
		{ID: "haiku", Name: "Haiku", Context: 200_000, Small: true},
	}, nil
}

// Usage reads the plan's windows with the /usage probe, and answers with the newest reading: the probe's, or a
// session's mod's ($.session.usage()) if that's newer.
func (p Provider) Usage(ctx context.Context) (core.ProviderUsage, error) {
	err := p.readUsage(ctx)
	usage.mu.Lock()
	u := usage.last
	usage.mu.Unlock()
	u.Provider = "claude"
	u.Plan, u.Tier = planOf(nil)
	u.Account, u.AccountName = account(nil)
	return u, err
}

// Logs are a session's log and its subagents' logs, where the host keeps them (a sandbox writes through a mount).
func (Provider) Logs(cwd, session string) []agent.Log {
	if session == "" {
		return nil
	}
	main := transcript.Path(transcript.ConfigDir(nil), cwd, session)
	out := []agent.Log{{Path: main, Format: "claude"}}
	for _, p := range transcript.Subagents(main) {
		out = append(out, agent.Log{Path: p, Format: "claude"})
	}
	return out
}

// windowNames are what the windows $.session.usage() reports by kind are called.
var windowNames = map[string]string{
	"five_hour":        "Current session",
	"seven_day":        "Current week (all models)",
	"seven_day_opus":   "Current week (Opus)",
	"seven_day_sonnet": "Current week (Sonnet)",
}

// windowLength is how long a window of a kind is: five_hour, and seven_day with its per-model variants.
func windowLength(kind string) time.Duration {
	switch {
	case strings.HasPrefix(kind, "five_hour"):
		return 5 * time.Hour
	case strings.HasPrefix(kind, "seven_day"):
		return 7 * 24 * time.Hour
	}
	return 0
}

func usageOf(rls []rateLimit) core.ProviderUsage {
	u := core.ProviderUsage{Provider: "claude", Sampled: time.Now(), Source: "/usage, through the Oriel mod ($.session.usage())"}
	for _, rl := range rls {
		name := windowNames[rl.Kind]
		if name == "" {
			name = strings.ReplaceAll(rl.Kind, "_", " ")
		}
		w := core.UsageWindow{Name: name, Used: rl.PercentUsed / 100, Length: windowLength(rl.Kind)}
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
	s.term.watch = s.watchScreen
	s.tail.answered = s.answered
	go s.term.drain()
	go s.serve()
	go s.tail.follow(s.done, s.push)
	go s.run()
	return s, nil
}
