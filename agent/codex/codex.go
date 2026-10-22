// Package codex is the Codex agent.Provider: `codex app-server` (protocol v2, JSON-RPC over JSONL on stdio),
// signed in with ChatGPT so turns are billed to the plan.
package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/semver"
)

// AllowAPIKey, set to 1 in StartSpec.Env, lets a session run on an API-key login, billed per token.
const AllowAPIKey = "ORIEL_CODEX_ALLOW_API_KEY"

// Provider runs Codex sessions.
type Provider struct {
	Bin      string // codex; "" finds it on PATH
	OrielBin string // the binary that serves `hook mcp`; "" is this one
	// ReleasesAPI is where a sandbox's codex is downloaded from: GitHub's API for openai/codex if "".
	ReleasesAPI string

	downloads sync.Mutex // one download at a time
}

var _ agent.Provider = (*Provider)(nil)

func (p *Provider) ID() string { return "codex" }

func (p *Provider) bin() string {
	if p.Bin != "" {
		return p.Bin
	}
	return "codex"
}

func (p *Provider) orielBin() string {
	if p.OrielBin != "" {
		return p.OrielBin
	}
	if exe, err := os.Executable(); err == nil {
		return exe
	}
	return "oriel"
}

// minVersion is the release the app-server protocol vendored in spec/codex is from (rust-v0.163.0).
const minVersion = "0.163.0"

// Features: a tool-less fork (Session.Fork), images as images, signing in from Oriel, a sandbox, and stopping the
// background terminals a turn leaves running. It has no terminal of its own here.
func (p *Provider) Features() agent.Features {
	return agent.Features{Fork: true, Images: true, Login: true, Sandbox: true, StopProcesses: true}
}

var _ agent.Featured = (*Provider)(nil)

// Detect finds the binary and its version, then asks a short-lived app-server who is signed in. A codex that isn't
// installed is a Note, not an error.
func (p *Provider) Detect(ctx context.Context) (agent.Install, error) {
	path, err := exec.LookPath(p.bin())
	if err != nil {
		return agent.Install{Bin: p.bin(), Note: "Codex is not installed, or not on PATH."}, nil
	}
	in := agent.Install{Bin: path}
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(vctx, path, "--version").Output()
	if err != nil {
		in.Note = "codex --version failed: " + err.Error()
		return in, nil
	}
	in.Version = parseVersion(string(out))
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := p.short(actx)
	if err != nil {
		in.Note = "couldn't ask codex who is signed in: " + err.Error()
		return in, nil
	}
	defer c.close()
	acct, err := readAccount(actx, c.rpc)
	if err != nil {
		in.Note = err.Error()
		return in, nil
	}
	in.LoggedIn = acct.Account != nil
	in.Account, in.Note = acct.describe()
	if in.LoggedIn {
		in.Login = loginMark(home())
	}
	if versionLess(in.Version, minVersion) {
		in.Note = fmt.Sprintf("Codex %s is too old for Oriel, which needs %s or newer: update codex.", in.Version, minVersion)
	}
	return in, nil
}

func versionLess(a, b string) bool {
	x, _, errA := semver.ParsePartial(a)
	y, _, errB := semver.ParsePartial(b)
	return errA == nil && errB == nil && x.Compare(y) < 0
}

// parseVersion takes "codex-cli 0.98.0" to "0.98.0".
func parseVersion(s string) string {
	f := strings.Fields(strings.TrimSpace(s))
	if len(f) == 0 {
		return ""
	}
	return f[len(f)-1]
}

// Models asks the app-server for its models, falling back to the catalog's. A model the catalog knows takes its
// context window and size from there: model/list says neither.
func (p *Provider) Models(ctx context.Context) ([]agent.Model, error) {
	if _, err := exec.LookPath(p.bin()); err != nil {
		return staticModels, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := p.short(ctx)
	if err != nil {
		return staticModels, nil
	}
	defer c.close()
	var r struct {
		Data []struct {
			Model                     string
			DisplayName               string
			Hidden                    bool
			IsDefault                 bool
			SupportedReasoningEfforts []struct{ ReasoningEffort string }
		}
	}
	if err := c.rpc.Call(ctx, "model/list", map[string]any{}, &r); err != nil || len(r.Data) == 0 {
		return staticModels, nil
	}
	var out []agent.Model
	for _, m := range r.Data {
		if m.Hidden {
			continue
		}
		am := agent.Model{ID: m.Model, Name: m.DisplayName, Default: m.IsDefault}
		for _, e := range m.SupportedReasoningEfforts {
			am.Efforts = append(am.Efforts, e.ReasoningEffort)
		}
		if i := slices.IndexFunc(staticModels, func(s agent.Model) bool { return s.ID == m.Model }); i >= 0 {
			am.Context, am.Small = staticModels[i].Context, staticModels[i].Small
		}
		out = append(out, am)
	}
	return out, nil
}

// staticModels are the listed models of Codex's bundled catalog (codex-rs/models-manager/models.json), by its
// priority: the first is the default, and the fast, affordable one is the small model.
var staticModels = []agent.Model{
	{ID: "gpt-6.1-sol", Name: "GPT-6.1-Sol", Efforts: effortsUltra, Context: 272_000, Default: true},
	{ID: "gpt-6-astra", Name: "GPT-6-Astra", Efforts: effortsUltra, Context: 272_000},
	{ID: "gpt-6-sol", Name: "GPT-6-Sol", Efforts: effortsUltra, Context: 272_000},
	{ID: "gpt-6-luna", Name: "GPT-6-Luna", Efforts: effortsMax, Context: 272_000, Small: true},
	{ID: "gpt-5.6-sol", Name: "GPT-5.6-Sol", Efforts: effortsUltra, Context: 272_000},
	{ID: "gpt-5.6-terra", Name: "GPT-5.6-Terra", Efforts: effortsUltra, Context: 272_000},
	{ID: "gpt-5.6-luna", Name: "GPT-5.6-Luna", Efforts: effortsMax, Context: 272_000},
	{ID: "gpt-5.5", Name: "GPT-5.5", Efforts: []string{"low", "medium", "high", "xhigh"}, Context: 272_000},
}

var (
	effortsMax   = []string{"low", "medium", "high", "xhigh", "max"}
	effortsUltra = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
)

// ProbeError is why Usage read no windows.
type ProbeError struct {
	Up  bool // the app-server came up and was asked
	Err error
}

func (e *ProbeError) Error() string {
	if !e.Up {
		return "codex: the app-server for the usage check didn't start: " + e.Err.Error()
	}
	return "codex: account/rateLimits/read: " + e.Err.Error()
}

func (e *ProbeError) Unwrap() error    { return e.Err }
func (e *ProbeError) Provider() string { return "codex" }
func (e *ProbeError) Started() bool    { return e.Up }

var _ agent.UsageProbeFailed = (*ProbeError)(nil)

// Usage reads the plan's rate limits from a short-lived app-server. With codex not installed there is nothing to
// read, and nothing went wrong.
func (p *Provider) Usage(ctx context.Context) (core.ProviderUsage, error) {
	if _, err := exec.LookPath(p.bin()); err != nil {
		return core.ProviderUsage{Provider: "codex"}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c, err := p.short(ctx)
	if err != nil {
		return core.ProviderUsage{}, &ProbeError{Err: err}
	}
	defer c.close()
	var r struct{ RateLimits rateLimits }
	if err := c.rpc.Call(ctx, "account/rateLimits/read", nil, &r); err != nil {
		return core.ProviderUsage{}, &ProbeError{Up: true, Err: err}
	}
	u := r.RateLimits.usage(time.Now())
	if acct, err := readAccount(ctx, c.rpc); err == nil {
		u.Account = acct.identity()
		u.AccountName = u.Account
	}
	return u, nil
}

var _ agent.Logged = (*Provider)(nil)

// Logs are a thread's rollouts in Codex's home, where the stats read each request.
func (p *Provider) Logs(cwd, session string) []agent.Log {
	if session == "" {
		return nil
	}
	var out []agent.Log
	for _, t := range p.Discover() {
		if t.Session == session {
			out = append(out, t.Logs...)
		}
	}
	slices.SortFunc(out, func(a, b agent.Log) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// userFiles are the parts of the host's Codex home a sandboxed agent reads.
var userFiles = []string{"AGENTS.md", "prompts", "skills"}

// shortServer is an app-server started for one question.
type shortServer struct {
	cmd *exec.Cmd
	rpc *jsonrpc.Conn
}

func (p *Provider) short(ctx context.Context) (*shortServer, error) {
	return p.serve(ctx, jsonrpc.Handlers{})
}

// serve starts an app-server that lives as long as ctx, with h answering what it sends.
func (p *Provider) serve(ctx context.Context, h jsonrpc.Handlers) (*shortServer, error) {
	cmd := exec.CommandContext(ctx, p.bin(), "app-server")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s := &shortServer{cmd: cmd, rpc: jsonrpc.NewConn(out, in, h, jsonrpc.Options{})}
	if err := initialize(ctx, s.rpc); err != nil {
		s.close()
		return nil, err
	}
	return s, nil
}

func (s *shortServer) close() {
	s.rpc.Close()
	if s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
	s.cmd.Wait()
}

func initialize(ctx context.Context, rpc *jsonrpc.Conn) error {
	params := map[string]any{
		"clientInfo":   map[string]any{"name": "oriel", "title": "Oriel", "version": "0"},
		"capabilities": map[string]any{"experimentalApi": true},
	}
	if err := rpc.Call(ctx, "initialize", params, nil); err != nil {
		return fmt.Errorf("codex app-server initialize: %w", err)
	}
	return rpc.Notify("initialized", nil)
}

// account is account/read's answer.
type account struct {
	Account *struct {
		Type     string `json:"type"`
		Email    string `json:"email"`
		PlanType string `json:"planType"`
	} `json:"account"`
	RequiresOpenaiAuth bool `json:"requiresOpenaiAuth"`
}

// identity is who's signed in, by email: the account the plan's windows belong to.
func (a account) identity() string {
	if a.Account == nil {
		return ""
	}
	return a.Account.Email
}

func readAccount(ctx context.Context, rpc *jsonrpc.Conn) (account, error) {
	var a account
	if err := rpc.Call(ctx, "account/read", map[string]any{"refreshToken": false}, &a); err != nil {
		return a, fmt.Errorf("codex account/read: %w", err)
	}
	return a, nil
}

// describe names the login for Install, and what to know before running on it.
func (a account) describe() (name, note string) {
	if a.Account == nil {
		return "", "not signed in: run `codex login` and choose ChatGPT"
	}
	switch a.Account.Type {
	case "chatgpt":
		return "chatgpt", ""
	case "apiKey":
		return "apiKey", "signed in with an API key: turns would be billed per token, not to a ChatGPT plan"
	}
	return a.Account.Type, "not a ChatGPT login: turns would be billed outside a ChatGPT plan"
}

// requireChatGPT refuses any login but ChatGPT unless env opts in to per-token billing.
func requireChatGPT(a account, env []string) error {
	if a.Account == nil {
		return signedOut("", "codex isn't signed in: run `codex login` and choose ChatGPT")
	}
	if a.Account.Type == "chatgpt" {
		return nil
	}
	if slices.Contains(env, AllowAPIKey+"=1") {
		return nil
	}
	what := "with an API key"
	if a.Account.Type != "apiKey" {
		what = "with " + a.Account.Type
	}
	return fmt.Errorf("codex is signed in %s: every turn would be billed per token, not to your ChatGPT plan. "+
		"Run `codex login` and choose ChatGPT, or set %s=1 to run on it anyway", what, AllowAPIKey)
}

// rateLimits is a RateLimitSnapshot.
type rateLimits struct {
	LimitID   string     `json:"limitId,omitempty"`
	Primary   *rateLimit `json:"primary,omitempty"`
	Secondary *rateLimit `json:"secondary,omitempty"`
	Credits   *struct {
		HasCredits bool    `json:"hasCredits"`
		Unlimited  bool    `json:"unlimited"`
		Balance    *string `json:"balance"`
	} `json:"credits,omitempty"`
	PlanType             string `json:"planType,omitempty"`
	RateLimitReachedType string `json:"rateLimitReachedType,omitempty"`
}

type rateLimit struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins *int64  `json:"windowDurationMins"`
	ResetsAt           *int64  `json:"resetsAt"` // unix seconds
}

// merge takes a sparse update over what was known: what it leaves out stays.
func (r rateLimits) merge(u rateLimits) rateLimits {
	if u.Primary != nil {
		r.Primary = u.Primary
	}
	if u.Secondary != nil {
		r.Secondary = u.Secondary
	}
	if u.Credits != nil {
		r.Credits = u.Credits
	}
	if u.PlanType != "" {
		r.PlanType = u.PlanType
	}
	r.RateLimitReachedType = u.RateLimitReachedType
	return r
}

func (r rateLimits) usage(now time.Time) core.ProviderUsage {
	u := core.ProviderUsage{Provider: "codex", Plan: planName(r.PlanType), Tier: tierName(r.PlanType), Sampled: now, Source: "Codex's account/rateLimits/read"}
	for i, w := range []*rateLimit{r.Primary, r.Secondary} {
		if w == nil {
			continue
		}
		uw := core.UsageWindow{Name: windowName(w.WindowDurationMins, i), Used: w.UsedPercent / 100}
		if w.WindowDurationMins != nil {
			uw.Length = time.Duration(*w.WindowDurationMins) * time.Minute
		}
		if w.ResetsAt != nil {
			uw.Resets = time.Unix(*w.ResetsAt, 0)
		}
		u.Windows = append(u.Windows, uw)
	}
	if c := r.Credits; c != nil {
		switch {
		case c.Unlimited:
			u.Credits = "unlimited"
		case c.Balance != nil && *c.Balance != "":
			u.Credits = *c.Balance
		case c.HasCredits:
			u.Credits = "yes"
		}
	}
	return u
}

// windowName is a window as Claude's are named: "Current session" for the five-hour one, "Current week" for the
// week's, by length when Codex says it.
func windowName(mins *int64, i int) string {
	if mins == nil {
		return []string{"Current session", "Current week"}[i]
	}
	switch m := *mins; {
	case m == 300:
		return "Current session"
	case m == 7*24*60:
		return "Current week"
	case m%(24*60) == 0:
		return fmt.Sprintf("%dd", m/(24*60))
	case m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

func planName(p string) string {
	if t := tierName(p); t != "" {
		return "ChatGPT " + t
	}
	return ""
}

// tierName is a planType as its chip reads: pro_lite is "Pro Lite".
func tierName(p string) string {
	words := strings.FieldsFunc(p, func(r rune) bool { return r == '_' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// tail keeps the last few KB a process wrote, for its error.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if n := len(t.buf); n > 4096 {
		t.buf = t.buf[n-4096:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.TrimSpace(t.buf))
}

func jsonOf(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}
