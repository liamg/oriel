// Package rig is the app's test fixtures that don't need the app: session hosts with fake providers, and the
// sessions, transcripts and plan readings the mockups show. Package app's own tests and feature packages' share it.
package rig

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/internal/isolate"
)

// NopExec runs nothing.
type NopExec struct{ agent.Exec }

// SteadyLoad is a machine's load that doesn't change, so a scene showing it draws the same each run.
func SteadyLoad() sysload.Reading {
	return sysload.Reading{CPU: 34, Cores: 12, MemUsed: 11 << 30, MemTotal: 32 << 30}
}

// WaitState polls the store until ok, failing the test after 10s.
func WaitState(t *testing.T, s core.Store, what string, ok func(core.State) bool) core.State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := s.State()
		if ok(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// StatsNow is the stats fixture's now: Wed Oct 14, 12:17.
var StatsNow = time.Date(2026, 10, 14, 12, 17, 0, 0, time.UTC)

// LoggedFake is a fake provider keeping its own log of each session, as Claude does: the fixture transcripts,
// by the directory the session's agent runs in, and those it finds on the machine.
type LoggedFake struct {
	*agenttest.Fake
	mu    sync.Mutex
	logs  map[string]string
	found []agent.Transcript
}

// NewLoggedFake is a LoggedFake with no logs yet.
func NewLoggedFake() *LoggedFake {
	return &LoggedFake{Fake: agenttest.NewFake(), logs: map[string]string{}}
}

// SetLog keeps p as the log of the session whose agent runs in cwd.
func (f *LoggedFake) SetLog(cwd, p string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs[cwd] = p
}

// Found adds a transcript it finds on the machine, of a session outside Oriel.
func (f *LoggedFake) Found(t agent.Transcript) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.found = append(f.found, t)
}

func (f *LoggedFake) Discover() []agent.Transcript {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.found)
}

func (f *LoggedFake) Logs(cwd, _ string) []agent.Log {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.logs[cwd]; ok {
		return []agent.Log{{Path: p, Format: stats.FormatClaude}}
	}
	return nil
}

// Stats is a session host with six repos and fixture sessions over them, each with a fixture transcript
// of a month's work (or, for Codex, Oriel's record of its events), and a week of plan readings.
type Stats struct {
	T      *testing.T
	W      *host.Host
	Home   string
	Claude *LoggedFake
	Codex  *agenttest.Fake
	Repos  map[string]string
	IDs    map[string]core.SessionID
	Logs   []string // every fixture log written
}

func NewStats(t *testing.T) *Stats {
	t.Helper()
	isolate.Test(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	r := &Stats{T: t, Home: t.TempDir(), Repos: map[string]string{}, IDs: map[string]core.SessionID{},
		Claude: NewLoggedFake(), Codex: agenttest.NewFake()}
	r.Claude.Name, r.Codex.Name = "claude", "codex"
	root := t.TempDir()
	for _, name := range []string{"pricing", "billing-ui", "admin-ui", "api", "web", "infra"} {
		g := &gittest.Repo{T: t, Dir: filepath.Join(root, name), When: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
		os.MkdirAll(g.Dir, 0o755)
		g.Git("init", "-q", "-b", "main")
		g.Write(map[string]string{"README": name + "\n"})
		g.Commit("Ada", "first")
		r.Repos[name] = g.Dir
	}
	providers := &agent.Providers{}
	providers.Register(r.Claude)
	providers.Register(r.Codex)
	w, err := host.New(host.Options{Home: r.Home, Providers: providers, Exec: NopExec{}, Load: SteadyLoad,
		Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.W = w
	return r
}

// FxSession is how a fixture session worked: its repos and their weights in its file calls, its model, the days it
// was busy and how hard.
type FxSession struct {
	Name, Provider, Model, Prompt string
	Repos                         []string
	Weights                       []int
	Days                          []int // days before StatsNow it worked on
	PerDay                        int   // requests on each
}

var FxSessions = []FxSession{
	{Name: "search-api", Provider: "claude", Model: "claude-opus-4-1", Repos: []string{"api", "web"}, Weights: []int{5, 2},
		Days: []int{0, 1, 2, 3, 5, 6, 8, 9}, PerDay: 70},
	{Name: "admin-filters", Provider: "claude", Model: "claude-opus-4-1", Repos: []string{"admin-ui", "api", "web"}, Weights: []int{4, 2, 1},
		Days: []int{0, 1, 4, 6, 7}, PerDay: 90},
	{Name: "flaky-e2e-sweep", Provider: "codex", Model: "gpt-5-codex", Prompt: "Sweep the flaky e2e tests", Repos: []string{"web", "infra"},
		Weights: []int{3, 2}, Days: []int{1, 3, 10, 12, 14, 16}, PerDay: 60},
	{Name: "invoice-pdf", Provider: "claude", Model: "claude-sonnet-4-5", Repos: []string{"billing-ui"}, Weights: []int{1},
		Days: []int{3, 4, 5, 13, 14, 15, 17}, PerDay: 40},
	{Name: "pricing-tiers", Provider: "claude", Model: "claude-opus-4-1", Repos: []string{"pricing"}, Weights: []int{1},
		Days: []int{18, 19, 20, 21, 23, 24, 25, 26, 27}, PerDay: 60},
	{Name: "infra-certs", Provider: "claude", Model: "claude-sonnet-4-5", Repos: []string{"infra"}, Weights: []int{1},
		Days: []int{11, 12, 22, 28}, PerDay: 30},
	{Name: "web-nav", Provider: "claude", Model: "claude-opus-4-1", Repos: []string{"web", "admin-ui"}, Weights: []int{3, 1},
		Days: []int{7, 8, 15, 16, 20, 21, 29}, PerDay: 50},
	{Name: "pricing-rounding", Provider: "claude", Model: "claude-opus-4-1", Prompt: "Round invoice lines half-to-even",
		Repos: []string{"pricing", "billing-ui"}, Weights: []int{214, 101}},
}

// Build starts the sessions and writes their logs.
func (r *Stats) Build() {
	t := r.T
	for i, fx := range FxSessions {
		var ids []core.RepoID
		for _, rp := range fx.Repos {
			ids = append(ids, core.RepoID(r.Repos[rp]))
		}
		id, err := r.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: fx.Name, Prompt: fx.Prompt, Provider: fx.Provider,
			Model: fx.Model, Effort: "high", Repos: ids})
		if err != nil {
			t.Fatal(err)
		}
		r.IDs[fx.Name] = id
		se := r.Wait(id)
		dirs := map[string]string{}
		for _, p := range se.Projects {
			dirs[p.Name] = p.Dir
		}
		rnd := rand.New(rand.NewSource(int64(i + 1)))
		var lines []string
		if fx.Name == "pricing-rounding" {
			lines = roundingLog(rnd, dirs)
		} else {
			for _, d := range fx.Days {
				lines = append(lines, DayLog(rnd, fx, dirs, StatsNow.AddDate(0, 0, -d), fx.Provider == "codex")...)
			}
		}
		body := []byte(strings.Join(lines, "\n") + "\n")
		if fx.Provider == "codex" {
			p := filepath.Join(r.Home, "sessions", string(id), "agent", "events.jsonl")
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, body, 0o644)
			r.Logs = append(r.Logs, p)
			continue
		}
		p := filepath.Join(t.TempDir(), string(id)+".jsonl")
		os.WriteFile(p, body, 0o644)
		r.Logs = append(r.Logs, p)
		r.Claude.SetLog(se.Dir, p)
	}
}

func (r *Stats) Wait(id core.SessionID) core.SessionState {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, se := range r.State() {
			if se.ID == id && len(se.Projects) > 0 {
				return se
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.T.Fatalf("no session %s", id)
	return core.SessionState{}
}

func (r *Stats) State() []core.SessionState { return r.W.Store().State().Sessions }

var tools = []struct {
	name string
	file bool
	ms   int
	errs int // in a hundred
}{{"Read", true, 100, 0}, {"Read", true, 120, 0}, {"Edit", true, 200, 3}, {"Bash", false, 4100, 9}, {"Grep", true, 150, 0},
	{"TodoWrite", false, 40, 0}, {"Glob", true, 60, 0}, {"Write", true, 300, 0}, {"Task", false, 130_000, 11}, {"WebFetch", false, 9_800, 50}}

// DayLog is a day's requests, the Claude log's records or Oriel's events for Codex.
func DayLog(rnd *rand.Rand, fx FxSession, dirs map[string]string, day time.Time, events bool) []string {
	at := time.Date(day.Year(), day.Month(), day.Day(), 9+rnd.Intn(3), rnd.Intn(60), 0, 0, time.UTC)
	if at.After(StatsNow) {
		at = StatsNow.Add(-3 * time.Hour)
	}
	ctx := int64(30_000)
	var out []string
	n := fx.PerDay/2 + rnd.Intn(fx.PerDay)
	for k := 0; k < n; k++ {
		if k%8 == 0 {
			out = append(out, Prompt(at, events))
		}
		ctx += int64(rnd.Intn(3000))
		out = append(out, Request(rnd, fx, dirs, at, ctx, k, events)...)
		at = at.Add(time.Duration(40+rnd.Intn(200)) * time.Second)
		if at.After(StatsNow) {
			break
		}
	}
	return out
}

// roundingLog is pricing-rounding's morning: 09:12 to 12:17 with two idle stretches and a compaction.
func roundingLog(rnd *rand.Rand, dirs map[string]string) []string {
	fx := FxSessions[len(FxSessions)-1]
	at := time.Date(2026, 10, 14, 9, 12, 0, 0, time.UTC)
	ctx := int64(40_000)
	var out []string
	k := 0
	for at.Before(StatsNow) {
		switch {
		case at.Hour() == 10 && at.Minute() >= 20 && at.Minute() < 46:
			at = time.Date(2026, 10, 14, 10, 46, 0, 0, time.UTC)
		case at.Hour() == 11 && at.Minute() >= 22 && at.Minute() < 38:
			at = time.Date(2026, 10, 14, 11, 38, 0, 0, time.UTC)
		}
		if k%11 == 0 {
			out = append(out, Prompt(at, false))
		}
		ctx += int64(800 + rnd.Intn(1200))
		if ctx > 168_000 {
			ctx = 41_000
		}
		out = append(out, Request(rnd, fx, dirs, at, ctx, k, false)...)
		if k%17 == 5 {
			agent := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"isSidechain":true,"agentId":"a%d","message":{"id":"s%d","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":2000,"output_tokens":900,"cache_read_input_tokens":25000}}}`,
				at.Add(5*time.Second).Format(time.RFC3339), k/17, k)
			out = append(out, agent)
		}
		at = at.Add(time.Duration(15+rnd.Intn(30)) * time.Second)
		k++
	}
	return out
}

// Prompt is a user's prompt in a log: Claude's record, or Oriel's event for Codex.
func Prompt(at time.Time, events bool) string {
	if events {
		return fmt.Sprintf(`{"at":%q,"kind":"turn"}`, at.Format(time.RFC3339))
	}
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":"carry on"}}`, at.Format(time.RFC3339))
}

// Request is one request with a tool call or two, and their results.
func Request(rnd *rand.Rand, fx FxSession, dirs map[string]string, at time.Time, ctx int64, k int, events bool) []string {
	pick := func() string {
		total := 0
		for _, w := range fx.Weights {
			total += w
		}
		n := rnd.Intn(total)
		for i, w := range fx.Weights {
			if n < w {
				return dirs[fx.Repos[i]]
			}
			n -= w
		}
		return dirs[fx.Repos[0]]
	}
	var calls []string
	var results []string
	var evs []string
	for c := 0; c < 1+rnd.Intn(2); c++ {
		tl := tools[rnd.Intn(len(tools))]
		id := fmt.Sprintf("t%d_%d", k, c)
		input := `{}`
		if tl.file {
			input = fmt.Sprintf(`{"file_path":%q}`, filepath.Join(pick(), "src", fmt.Sprintf("f%d.go", rnd.Intn(9))))
		}
		done := at.Add(time.Duration(tl.ms/2+rnd.Intn(tl.ms+1)) * time.Millisecond)
		bad := rnd.Intn(100) < tl.errs
		if events {
			evs = append(evs, fmt.Sprintf(`{"at":%q,"kind":"tool","id":%q,"name":%q,"input":%s}`, at.Format(time.RFC3339Nano), id, tl.name, input),
				fmt.Sprintf(`{"at":%q,"kind":"result","id":%q,"error":%v}`, done.Format(time.RFC3339Nano), id, bad))
			continue
		}
		calls = append(calls, fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":%s}`, id, tl.name, input))
		results = append(results, fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":%q,"is_error":%v}]}}`,
			done.Format(time.RFC3339Nano), id, bad))
	}
	in, out, write := int64(200+rnd.Intn(800)), int64(400+rnd.Intn(1600)), int64(1000+rnd.Intn(4000))
	if events {
		return append(evs, fmt.Sprintf(`{"at":%q,"kind":"request","model":%q,"effort":"high","tokens":{"input":%d,"output":%d,"cacheRead":%d}}`,
			at.Add(2*time.Second).Format(time.RFC3339), fx.Model, in+write, out*2, ctx*2))
	}
	effort := []string{"high", "high", "high", "medium", "low"}[rnd.Intn(5)]
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"effort":%q,"message":{"id":"m%d","model":%q,"content":[%s],`+
		`"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		at.Format(time.RFC3339), effort, k, fx.Model, strings.Join(calls, ","), in, out, ctx, write, write*2/3, write/3)
	return append([]string{line}, results...)
}

// Readings is a week of plan readings, ending now: Claude's 5-hour, weekly and weekly Opus windows, and Codex's.
func (r *Stats) Readings(t *testing.T) {
	t.Helper()
	claude := r.Claude.WaitStarted(t)
	codex := r.Codex.WaitStarted(t)
	weekReset := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	at := StatsNow.Add(-7 * 24 * time.Hour)
	for ; !at.After(StatsNow); at = at.Add(time.Hour) {
		reset := weekReset
		if at.Before(weekReset.Add(-week)) {
			reset = weekReset.Add(-week)
		}
		opened := reset.Add(-week)
		h := at.Sub(opened).Hours()
		dayAgo := StatsNow.Add(-24 * time.Hour)
		weekly := h * .0036
		switch {
		case reset == weekReset && at.After(dayAgo):
			weekly = .446 + at.Sub(dayAgo).Hours()*.011
		case reset == weekReset:
			weekly = .446 * h / dayAgo.Sub(opened).Hours()
		}
		fiveReset := opened.Add(time.Duration(int(at.Sub(opened).Hours()/5)+1) * 5 * time.Hour)
		five := .1 + .3*float64(int(at.Sub(opened).Hours())%5)/5*float64(1+int(at.Hour())%3)/3
		if at.Equal(StatsNow.Truncate(time.Hour)) || at.After(StatsNow.Add(-time.Hour)) {
			fiveReset, five = time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), .31
		}
		claude.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "claude", Account: WorkAccount, AccountName: "ada@acme.example",
			Plan: "Claude Max", Tier: "Max 20×", Sampled: at, Source: "/usage, through the Oriel mod",
			Windows: []core.UsageWindow{
				{Name: "Current session", Used: five, Resets: fiveReset, Length: 5 * time.Hour},
				{Name: "Current week (all models)", Used: min(weekly, 1), Resets: reset, Length: week},
				{Name: "Current week (Opus)", Used: min(weekly*.82, 1), Resets: reset, Length: week},
			}}})
		codex.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "codex", Account: "ada@example.com", AccountName: "ada@example.com",
			Plan: "ChatGPT Pro", Sampled: at, Source: "Codex's account/rateLimits/read",
			Windows: []core.UsageWindow{
				{Name: "5h", Used: .22 * at.Sub(StatsNow.Add(-7*24*time.Hour)).Hours() / (7 * 24), Resets: time.Date(2026, 10, 14, 15, 27, 0, 0, time.UTC),
					Length: 5 * time.Hour},
				{Name: "weekly", Used: .48 * at.Sub(StatsNow.Add(-7*24*time.Hour)).Hours() / (7 * 24), Resets: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC),
					Length: week},
			}}})
	}
	last := func(u core.ProviderUsage) {
		claude.Emit(agent.UsageUpdate{Usage: u})
	}
	last(core.ProviderUsage{Provider: "claude", Account: WorkAccount, AccountName: "ada@acme.example", Plan: "Claude Max", Tier: "Max 20×",
		Sampled: StatsNow.Add(-30 * time.Minute), Source: "/usage, through the Oriel mod",
		Windows: []core.UsageWindow{
			{Name: "Current session", Used: .31, Resets: time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), Length: 5 * time.Hour},
			{Name: "Current week (all models)", Used: .705, Resets: weekReset, Length: week},
			{Name: "Current week (Opus)", Used: .575, Resets: weekReset, Length: week},
		}})
	last(core.ProviderUsage{Provider: "claude", Account: WorkAccount, AccountName: "ada@acme.example", Plan: "Claude Max", Tier: "Max 20×",
		Sampled: StatsNow.Add(-40 * time.Second), Source: "/usage, through the Oriel mod",
		Windows: []core.UsageWindow{
			{Name: "Current session", Used: .38, Resets: time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), Length: 5 * time.Hour},
			{Name: "Current week (all models)", Used: .71, Resets: weekReset, Length: week},
			{Name: "Current week (Opus)", Used: .58, Resets: weekReset, Length: week},
		}})
}

// WorkAccount is the fixture's Claude login: the account and organisation of a company subscription.
const WorkAccount = "u1/o-acme"

// Host is a session host with the fake provider and a fixture repo.
type Host struct {
	W    *host.Host
	Fake *agenttest.Fake
	Repo string
}

// NewHost is a session host with the fake provider, or the fakes given (named as the providers they stand for).
func NewHost(t *testing.T, fakes ...*agenttest.Fake) *Host {
	t.Helper()
	return NewHostWith(t, host.Options{}, fakes...)
}

// NewHostWith is NewHost with options of its own: its repos directory, its clock.
func NewHostWith(t *testing.T, o host.Options, fakes ...*agenttest.Fake) *Host {
	t.Helper()
	isolate.Test(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	repo := filepath.Join(t.TempDir(), "pricing")
	for _, args := range [][]string{{"init", "-q", "-b", "main", repo}, {"-C", repo, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	h := &Host{Fake: agenttest.NewFake(), Repo: repo}
	providers := &agent.Providers{}
	if len(fakes) == 0 {
		fakes = []*agenttest.Fake{h.Fake}
	}
	for _, f := range fakes {
		providers.Register(f)
	}
	if o.Home == "" {
		o.Home = t.TempDir()
	}
	o.Providers = providers
	if o.Exec == nil {
		o.Exec = NopExec{}
	}
	o.Packs = func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }
	if o.Load == nil {
		o.Load = SteadyLoad
	}
	if o.GitHub == nil {
		hub := ghtest.New(t) // never the user's own gh
		o.GitHub = func(string, []string) gh.Client { return hub.Client() }
	}
	w, err := host.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	h.W = w
	return h
}

// Changed counts the lines a edit removed and added, by longest common subsequence.
func Changed(a, b string) (removed, added int) {
	x, y := strings.Split(a, "\n"), strings.Split(b, "\n")
	lcs := make([][]int, len(x)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(y)+1)
	}
	for i := len(x) - 1; i >= 0; i-- {
		for j := len(y) - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	return len(x) - lcs[0][0], len(y) - lcs[0][0]
}

// WriteFile writes src to path, making its directory.
func WriteFile(t *testing.T, path, src string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ReadFile is the text at path.
func ReadFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// OverviewNow is the fixed clock the overview scenes are drawn at.
var OverviewNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

// GitRepo makes a repo at dir with files, and commits by authors on fixed days before OverviewNow.
func GitRepo(t testing.TB, dir string, files map[string]string, commits int) {
	t.Helper()
	run := func(env []string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), append([]string{"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null"}, env...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(dir, 0o755)
	run(nil, "init", "-q", "-b", "fix/stale-results")
	for name, body := range files {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
	}
	run(nil, "add", ".")
	authors := []string{"Liam Galvin", "Claude", "Alex Rivera", "Sam Kim", "Jo Park"}
	for i := range commits {
		who := authors[(i*i+i/3)%len(authors)]
		at := OverviewNow.Add(-time.Duration((i*37)%360) * 24 * time.Hour).Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)
		run([]string{"GIT_AUTHOR_NAME=" + who, "GIT_AUTHOR_EMAIL=a@x", "GIT_COMMITTER_NAME=" + who, "GIT_COMMITTER_EMAIL=a@x",
			"GIT_AUTHOR_DATE=" + at, "GIT_COMMITTER_DATE=" + at}, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("Change %d", commits-i))
	}
}

// WalkAnswer is a walkthrough as an agent might write it: a sentence first, a step naming its file by the end of its
// path, lines written three ways, a step with no lines, a summary, and trailing commas.
const WalkAnswer = "Here's the walkthrough.\n\n```json\n" + `{"steps": [
 {"title": "Ties round to even", "project": "pricing", "file": "round.go", "lines": [4, 18],
  "prose": "The rounding function now looks at ` + "`diff`" + ` once.",
  "notes": [
   {"title": "Difference computed once", "lines": [7, 8], "text": "` + "`diff`" + ` is worked out before the switch."},
   {"title": "Ties go to even", "lines": "L11-15", "mark": "t.IntPart()%2 == 0", "text": "An even integer part rounds down."}]},
 {"title": "Invoices round each line once", "project": "pricing", "file": "billing/invoice.go", "lines": [4, 11],
  "prose": "Exactly one place: ` + "`NewLine`" + `.",
  "notes": [
   {"title": "Rounded once, at creation", "lines": [6, 9], "text": "` + "`RoundMinor`" + ` can fail."},
   {"title": "The mode is stored", "lines": [10], "mark": "Rounding: pricing.HalfEven", "text": "Each line records how it was rounded."}]},
 {"title": "The total sums rounded lines", "project": "billing-ui", "file": "Total.tsx", "prose": "The UI adds what the server rounded."},
 {"title": "Summary", "prose": "Lines round once, half to even, and remember how.",},
]}` + "\n```\n"

// NoticeTexts are notifications' titles and texts, for a failure's message.
func NoticeTexts(ns []core.Notification) []string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Title+": "+n.Text)
	}
	return out
}

// PluginDir lays out a plugin's files in a directory of the test's.
func PluginDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		WriteFile(t, filepath.Join(dir, filepath.FromSlash(name)), body)
	}
	return dir
}

// WritableAfter makes dir writable again when the test ends, so installed plugins, kept read-only, can go.
func WritableAfter(t *testing.T, dir string) string {
	t.Cleanup(func() {
		filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	return dir
}

// Answered replies to each thread waiting on the fake agent a, as it would.
func Answered(t *testing.T, store core.Store, a *agenttest.FakeSession) {
	t.Helper()
	// The agent's id reaches the session when the host reads its Ready, which can trail what it was sent.
	st := WaitState(t, store, "the agent's session", func(st core.State) bool {
		for _, se := range st.Sessions {
			if se.Agent.Session == a.ID() {
				return true
			}
		}
		return false
	})
	for _, se := range st.Sessions {
		if se.Agent.Session != a.ID() {
			continue
		}
		for _, th := range se.Threads {
			if th.WaitsOnAgent() {
				a.Call(t, "reply", map[string]any{"thread": string(th.ID), "body": "Done."})
			}
		}
	}
}

// ToolInput is a tool call's input of m.
func ToolInput(m map[string]string) json.RawMessage {
	b, _ := json.Marshal(m)
	return b
}
