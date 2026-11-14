package claude

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/claudebox"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/eventq"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/semver"
)

// Provider is Claude Code, run as an interactive claude in a PTY with Oriel's mod: the subscription's way in.
type Provider struct {
	Bin      string // claude; "" is claude on PATH
	OrielBin string // the binary the mod runs as `<OrielBin> hook <op>`; "" is this executable
	ProbeDir string // a folder of Oriel's that the /usage probe runs claude in; "" is one in the temp dir
	// SandboxVersion is the Claude Code version a container sandbox runs; "" or nil runs the host's.
	SandboxVersion func() string
	// UsageURL is where the plan's windows are read first (UsageAPI if ""), with HTTP (http.DefaultClient if nil).
	UsageURL string
	HTTP     *http.Client

	usage usageBook
	box   claudebox.Box
}

var _ agent.Provider = (*Provider)(nil)

// minVersion is the oldest Claude Code with the function hooks the mod uses.
const minVersion = "2.1.284"

// The PTY's size until a view says otherwise.
const cols, rows = 200, 50

func (*Provider) ID() string { return "claude" }

func (p *Provider) bin() string {
	if p.Bin != "" {
		return p.Bin
	}
	return "claude"
}

var versionPattern = regexp.MustCompile(`\d+\.\d+\.\d+`)

func (p *Provider) Detect(ctx context.Context) (agent.Install, error) {
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
	in.Version = versionPattern.FindString(string(out))
	if in.Version == "" {
		in.Note = fmt.Sprintf("Could not read a version from claude --version: %q.", strings.TrimSpace(string(out)))
		return in, nil
	}
	in.LoggedIn, in.Account, in.Login = login(nil)
	// The Keychain holds the token where the files don't: read it afresh, so a login renewed since tells, and hand it
	// to the sandboxes.
	if in.LoggedIn && in.Account != "apiKey" {
		if l, ok := p.box.FreshHostLogin(); ok {
			if in.Login == "" {
				in.Login = tokenMark(l.AccessToken())
			}
			claudebox.SyncLogins(l)
		}
	}
	switch {
	case semver.Less(in.Version, minVersion):
		in.Note = fmt.Sprintf("Claude Code %s is too old for Oriel, which needs %s or newer: run claude update.", in.Version, minVersion)
	case !in.LoggedIn:
		in.Note = "Not signed in: run claude and /login."
	case in.Account == "apiKey":
		in.Note = "Signed in with an API key: billed per token, not to a subscription."
	}
	return in, nil
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
// organizationRateLimitTier in the state file (macOS keeps the token in the Keychain, so there's no mark: Detect
// reads it there).
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
		return cred.OAuth.Subscription, cred.OAuth.Tier, tokenMark(cred.OAuth.Token), true
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

// tokenMark tells one token from the next without holding it.
func tokenMark(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:8])
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
	return id, cmp.Or(a.Email, a.OrgName)
}

func plan(kind string) string {
	if kind == "" {
		return ""
	}
	return "Claude " + strings.ToUpper(kind[:1]) + kind[1:]
}

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

func (*Provider) Models(context.Context) ([]agent.Model, error) {
	return []agent.Model{
		{ID: "opus", Name: "Opus", Efforts: efforts, Context: 200_000, Default: true},
		{ID: "opus[1m]", Name: "Opus (1M context)", Efforts: efforts, Context: 1_000_000},
		{ID: "sonnet", Name: "Sonnet", Efforts: efforts, Context: 200_000},
		{ID: "sonnet[1m]", Name: "Sonnet (1M context)", Efforts: efforts, Context: 1_000_000},
		{ID: "haiku", Name: "Haiku", Context: 200_000, Small: true},
	}, nil
}

// Usage reads the plan's windows from the usage API with the host's sign-in, else with the /usage probe, and answers
// with the newest reading: that, or a session's mod's ($.session.usage()) if that's newer.
func (p *Provider) Usage(ctx context.Context) (core.ProviderUsage, error) {
	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	var err error
	if u, ok, aerr := p.apiUsage(ctx, client, cmp.Or(p.UsageURL, UsageAPI)); ok && aerr == nil {
		p.usage.mu.Lock()
		if u.Sampled.After(p.usage.last.Sampled) {
			p.usage.last = u
		}
		p.usage.mu.Unlock()
	} else {
		err = p.readUsage(ctx)
	}
	p.usage.mu.Lock()
	u := p.usage.last
	p.usage.mu.Unlock()
	u.Provider = "claude"
	u.Plan, u.Tier = planOf(nil)
	u.Account, u.AccountName = account(nil)
	return u, err
}

// Logs are a session's log and its subagents' logs, where the host keeps them (a sandbox writes through a mount).
func (*Provider) Logs(cwd, session string) []agent.Log {
	if session == "" {
		return nil
	}
	main := transcript.Path(transcript.ConfigDir(nil), cwd, session)
	out := []agent.Log{{Path: main, Format: UsageLog.Name}}
	for _, p := range transcript.Subagents(main) {
		out = append(out, agent.Log{Path: p, Format: UsageLog.Name})
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

func (p *Provider) Start(ctx context.Context, s agent.StartSpec) (agent.Session, error) {
	return p.start(ctx, s, "")
}

func (p *Provider) Resume(ctx context.Context, id string, s agent.StartSpec) (agent.Session, error) {
	if id == "" {
		return nil, errors.New("claude: no session id to resume")
	}
	return p.start(ctx, s, id)
}

// permissionMode is the agent's own gate for a policy. In a sandbox there's none: the sandbox and its gateway are the
// gate, and a prompt in Claude's own terminal would only stall the turn where nobody sees it.
func permissionMode(s agent.StartSpec) (string, error) {
	if s.Sandbox != nil {
		switch s.Policy {
		case agent.PolicyAsk, agent.PolicyLight, agent.PolicyBypass, agent.PolicyAuto, "":
			return "bypassPermissions", nil
		}
	}
	// dontAsk: "Don't prompt for permissions, deny if not pre-approved" (Claude Code's own words for it).
	switch s.Policy {
	case agent.PolicyRefuse:
		return "dontAsk", nil
	case agent.PolicyAsk, "":
		return "default", nil
	case agent.PolicyLight:
		return "acceptEdits", nil
	case agent.PolicyAuto:
		return "auto", nil
	case agent.PolicyBypass:
		if s.Sandbox == nil {
			return "", errors.New("claude: bypass is only for a sandboxed session: outside one, nothing would gate the agent")
		}
		return "bypassPermissions", nil
	}
	return "", fmt.Errorf("claude: unknown policy %q", s.Policy)
}

// resumesIn says a resumed conversation starts again in the mode it was last in: one of Claude's modes
// (PermissionMode in its mod API's types), and bypassPermissions only where a sandbox gates the agent.
func resumesIn(s agent.StartSpec, resume string) bool {
	switch s.Mode {
	case "default", "acceptEdits", "plan", "auto", "dontAsk":
		return resume != ""
	case "bypassPermissions":
		return resume != "" && s.Sandbox != nil
	}
	return false
}

// args is claude's command line. A fresh session starts on its pack's skill; a resumed one has it already.
func args(s agent.StartSpec, modDir, id, resume string) ([]string, error) {
	mode, err := permissionMode(s)
	if err != nil {
		return nil, err
	}
	if resumesIn(s, resume) {
		mode = s.Mode
	}
	a := []string{"--plugin-dir", modDir, "--permission-mode", mode}
	if s.Model != "" {
		a = append(a, "--model", s.Model)
	}
	if s.Effort != "" {
		a = append(a, "--effort", s.Effort)
	}
	switch {
	case s.Sandbox != nil && s.Sandbox.GatewayMCP != "":
		// Not strict: the user's claude.ai connectors load beside the gateway's servers, and a repository's own run
		// inside the sandbox. Every call to any of them asks Oriel (guard.Check), and the sandbox holds what they reach.
		a = append(a, "--mcp-config", s.Sandbox.GatewayMCP)
		if s.NoMCP {
			a = append(a, "--strict-mcp-config") // the gateway's alone: Oriel's own tools, and nothing of the user's
		}
	case s.NoMCP:
		a = append(a, "--mcp-config", `{"mcpServers":{}}`, "--strict-mcp-config")
	}
	if set := settings(s); len(set) > 0 {
		b, err := json.Marshal(set)
		if err != nil {
			return nil, err
		}
		a = append(a, "--settings", string(b))
	}
	if resume != "" {
		return append(a, "--resume", resume), nil
	}
	a = append(a, "--session-id", id)
	first := strings.TrimSpace(s.Prompt)
	if submitsFirst(s, resume) {
		first = ""
	}
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

// settings are the session's own settings on claude's command line, where nothing is written to the user's own:
// Oriel's classic hooks, its memory directory (a sandbox's settings.json has it already), and under PolicyRefuse
// what's allowed ahead, since dontAsk refuses everything else: Oriel's tools and the spec's Allow.
func settings(s agent.StartSpec) map[string]any {
	set := map[string]any{"hooks": classicHooks()}
	if s.Memory != "" && s.Sandbox == nil {
		set["autoMemoryDirectory"] = s.Memory
	}
	if s.Policy == agent.PolicyRefuse {
		set["permissions"] = map[string]any{"allow": append([]string{"mcp__oriel"}, s.Allow...)}
	}
	if s.Sandbox != nil && s.Sandbox.GatewayMCP != "" && !s.NoMCP {
		// A repository's servers would wait on Claude's approval dialog, which nobody at the sandbox's terminal sees.
		set["enableAllProjectMcpServers"] = true
	}
	return set
}

// classicHooks are Oriel's settings hooks. Claude Code runs settings hooks beneath every plugin: a plugin seated
// above Oriel's mod that skips the plugins people install (its built-in sec-default, seated on Team and Enterprise
// plans) skips the mod's classic hooks, but not these. Each hands its input to the session (op classic); a
// permission waits as long as the mod's asks do.
func classicHooks() map[string]any {
	hook := func(timeout int) []any {
		return []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": `"$ORIEL_BIN" hook classic`, "timeout": timeout}}}}
	}
	return map[string]any{
		"PermissionRequest": hook(600),
		"Stop":              hook(30),
		"StopFailure":       hook(30),
		"UserPromptSubmit":  hook(30),
	}
}

// maxArgPrompt is the longest first prompt claude's command line carries, under Linux's 128 KiB an argument.
const maxArgPrompt = 96 << 10

// submitsFirst says the first prompt is submitted through the mod once claude is up, not on its command line: a
// resumed session's, or one too long for it.
func submitsFirst(s agent.StartSpec, resume string) bool {
	p := strings.TrimSpace(s.Prompt)
	return p != "" && (resume != "" || len(p) > maxArgPrompt)
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func (p *Provider) start(ctx context.Context, spec agent.StartSpec, resume string) (_ agent.Session, err error) {
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
	cmd.Env = append(inherited(os.Environ()), processEnv(spec, sockPath(dir), orielBin)...)

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
	prices := price.Or(spec.Prices)
	s := &session{
		prices: prices,
		usage:  &p.usage,
		spec:   spec,
		ctx:    sctx,
		cancel: cancel,
		pty:    pty,
		term:   newTerminal(pty),
		ln:     ln,
		dir:    dir,
		temp:   temp,
		tail:   newTailer(config, spec.Cwd, id, resume != "", prices),
		events: eventq.New(),
		done:   make(chan struct{}),
		id:     id,
		wake:   make(chan struct{}, 1),
		asks:   map[string]chan agent.Decision{},
		forks:  map[string]chan forkAnswerIn{},
		heard:  time.Now(),

		unattendedWait: unattendedWait,
	}
	if submitsFirst(spec, resume) {
		s.cmds = append(s.cmds, waitOut{Kind: "submit", Text: strings.TrimSpace(spec.Prompt)})
	}
	s.term.watch = s.watchScreen
	s.term.typed = s.typed
	s.tail.answered = s.answered
	if spec.Sandbox != nil {
		s.keep = p.box.KeepLogins(func(ctx context.Context) { _ = p.readUsage(ctx) })
	}
	go s.term.drain()
	go s.serve()
	go s.tail.follow(s.done, s.push)
	go s.run()
	return s, nil
}

// inherited is Oriel's environment as claude gets it: without a parent Claude Code session's markers
// (claudebox.ParentSession), which would have claude take itself for that session's child and keep no transcript.
func inherited(environ []string) []string {
	return slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(claudebox.ParentSession, func(p string) bool { ok, _ := path.Match(p, k); return ok })
	})
}

// processEnv is what claude's environment adds to Oriel's own: the mod's way back, claude.ai's connectors off for a
// session without MCP servers, and for an agent nobody watches, unattendedEnv. spec.Env comes last, so a caller's own
// value stands. A sandbox keeps the connectors: every MCP call asks Oriel (guard.Check), bypassPermissions or not.
func processEnv(spec agent.StartSpec, sock, orielBin string) []string {
	env := []string{"ORIEL_SPAWNED=1", "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS=1", agent.HookSocketEnv + "=" + sock, "ORIEL_BIN=" + orielBin}
	if spec.NoMCP {
		env = append(env, "ENABLE_CLAUDEAI_MCP_SERVERS=false")
	}
	if spec.Unattended {
		env = append(env, "ORIEL_UNATTENDED=1")
		env = append(env, unattendedEnv...)
	}
	return append(env, spec.Env...)
}

// unattendedEnv turns off what Claude Code's terminal offers someone at it, each as Claude Code 2.1.322 reads it:
// the feedback survey that waits on a key (i.CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY), prompt suggestions
// (CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION===!1), the summary shown on coming back (cs(CLAUDE_CODE_ENABLE_AWAY_SUMMARY)),
// cost warnings (i.DISABLE_COST_WARNINGS), the installation notices (Pe(DISABLE_INSTALLATION_CHECKS)) and updating
// itself mid-run (Pe(DISABLE_AUTOUPDATER)).
var unattendedEnv = []string{
	"CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY=1",
	"CLAUDE_CODE_ENABLE_PROMPT_SUGGESTION=false",
	"CLAUDE_CODE_ENABLE_AWAY_SUMMARY=0",
	"DISABLE_COST_WARNINGS=1",
	"DISABLE_INSTALLATION_CHECKS=1",
	"DISABLE_AUTOUPDATER=1",
}

var _ agent.WebTooled = (*Provider)(nil)

// WebTools are Claude Code's tools that reach the web, which its gate asks about.
func (*Provider) WebTools() []string { return []string{"WebSearch", "WebFetch"} }
