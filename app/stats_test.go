package app

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
	"github.com/liamg/oriel/worker/stats"
)

// statsNow is mockup 43's: Wed Oct 14, 12:17.
var statsNow = time.Date(2026, 10, 14, 12, 17, 0, 0, time.UTC)

// loggedFake is a fake provider keeping its own log of each session, as Claude does: the fixture transcripts,
// by the directory the session's agent runs in.
type loggedFake struct {
	*agenttest.Fake
	mu   sync.Mutex
	logs map[string]string
}

func (f *loggedFake) Logs(cwd, _ string) []agent.Log {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.logs[cwd]; ok {
		return []agent.Log{{Path: p, Format: stats.FormatClaude}}
	}
	return nil
}

// statsRig is a worker with six repos and the sessions of mockup 43 over them, each with a fixture transcript of a
// month's work (or, for Codex, the worker's record of its events), and a week of plan readings.
type statsRig struct {
	t      *testing.T
	w      *worker.Worker
	home   string
	claude *loggedFake
	codex  *agenttest.Fake
	repos  map[string]string
	ids    map[string]core.SessionID
}

func newStatsRig(t *testing.T) *statsRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	r := &statsRig{t: t, home: t.TempDir(), repos: map[string]string{}, ids: map[string]core.SessionID{},
		claude: &loggedFake{Fake: agenttest.NewFake(), logs: map[string]string{}}, codex: agenttest.NewFake()}
	r.claude.Name, r.codex.Name = "claude", "codex"
	root := t.TempDir()
	for _, name := range []string{"pricing", "billing-ui", "admin-ui", "api", "web", "infra"} {
		g := &gittest.Repo{T: t, Dir: filepath.Join(root, name), When: time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)}
		os.MkdirAll(g.Dir, 0o755)
		g.Git("init", "-q", "-b", "main")
		g.Write(map[string]string{"README": name + "\n"})
		g.Commit("Ada", "first")
		r.repos[name] = g.Dir
	}
	providers := &agent.Providers{}
	providers.Register(r.claude)
	providers.Register(r.codex)
	w, err := worker.New(worker.Options{Home: r.home, CritHome: "-", Name: "mbp", Providers: providers, Exec: nopExec{},
		Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.w = w
	return r
}

func (r *statsRig) connect(t *testing.T) func(*Config) {
	return func(cfg *Config) {
		a, b := wire.Pipe()
		r.w.Serve(b)
		t.Cleanup(func() { a.Close(); b.Close() })
		cfg.Workers = append(cfg.Workers, worker.Client{Conn: a})
	}
}

// fxSession is how a fixture session worked: its repos and their weights in its file calls, its model, the days it
// was busy and how hard.
type fxSession struct {
	name, provider, model, prompt string
	repos                         []string
	weights                       []int
	days                          []int // days before statsNow it worked on
	perDay                        int   // requests on each
}

var fxSessions = []fxSession{
	{name: "usage-api", provider: "claude", model: "claude-opus-4-1", repos: []string{"api", "web"}, weights: []int{5, 2},
		days: []int{0, 1, 2, 3, 5, 6, 8, 9}, perDay: 70},
	{name: "admin-filters", provider: "claude", model: "claude-opus-4-1", repos: []string{"admin-ui", "api", "web"}, weights: []int{4, 2, 1},
		days: []int{0, 1, 4, 6, 7}, perDay: 90},
	{name: "flaky-e2e-sweep", provider: "codex", model: "gpt-5-codex", prompt: "Sweep the flaky e2e tests", repos: []string{"web", "infra"},
		weights: []int{3, 2}, days: []int{1, 3, 10, 12, 14, 16}, perDay: 60},
	{name: "invoice-pdf", provider: "claude", model: "claude-sonnet-4-5", repos: []string{"billing-ui"}, weights: []int{1},
		days: []int{3, 4, 5, 13, 14, 15, 17}, perDay: 40},
	{name: "pricing-tiers", provider: "claude", model: "claude-opus-4-1", repos: []string{"pricing"}, weights: []int{1},
		days: []int{18, 19, 20, 21, 23, 24, 25, 26, 27}, perDay: 60},
	{name: "infra-certs", provider: "claude", model: "claude-sonnet-4-5", repos: []string{"infra"}, weights: []int{1},
		days: []int{11, 12, 22, 28}, perDay: 30},
	{name: "web-nav", provider: "claude", model: "claude-opus-4-1", repos: []string{"web", "admin-ui"}, weights: []int{3, 1},
		days: []int{7, 8, 15, 16, 20, 21, 29}, perDay: 50},
	{name: "pricing-rounding", provider: "claude", model: "claude-opus-4-1", prompt: "Round invoice lines half-to-even",
		repos: []string{"pricing", "billing-ui"}, weights: []int{214, 101}},
}

// build starts the sessions and writes their logs.
func (r *statsRig) build() {
	t := r.t
	for i, fx := range fxSessions {
		var ids []core.RepoID
		for _, rp := range fx.repos {
			ids = append(ids, core.RepoID(r.repos[rp]))
		}
		id, err := r.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: fx.name, Prompt: fx.prompt, Provider: fx.provider,
			Model: fx.model, Effort: "high", Repos: ids})
		if err != nil {
			t.Fatal(err)
		}
		r.ids[fx.name] = id
		se := r.wait(id)
		dirs := map[string]string{}
		for _, p := range se.Projects {
			dirs[p.Name] = p.Dir
		}
		rnd := rand.New(rand.NewSource(int64(i + 1)))
		var lines []string
		if fx.name == "pricing-rounding" {
			lines = roundingLog(rnd, dirs)
		} else {
			for _, d := range fx.days {
				lines = append(lines, dayLog(rnd, fx, dirs, statsNow.AddDate(0, 0, -d), fx.provider == "codex")...)
			}
		}
		body := []byte(strings.Join(lines, "\n") + "\n")
		if fx.provider == "codex" {
			p := filepath.Join(r.home, "sessions", string(id), "agent", "events.jsonl")
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, body, 0o644)
			continue
		}
		p := filepath.Join(t.TempDir(), string(id)+".jsonl")
		os.WriteFile(p, body, 0o644)
		r.claude.mu.Lock()
		r.claude.logs[se.Dir] = p
		r.claude.mu.Unlock()
	}
}

func (r *statsRig) wait(id core.SessionID) core.SessionState {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, se := range r.state() {
			if se.ID == id && len(se.Projects) > 0 {
				return se
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	r.t.Fatalf("no session %s", id)
	return core.SessionState{}
}

func (r *statsRig) state() []core.SessionState {
	a, b := wire.Pipe()
	defer a.Close()
	defer b.Close()
	r.w.Serve(b)
	ss, _ := worker.Client{Conn: a}.Sessions(context.Background())
	return ss
}

var tools = []struct {
	name string
	file bool
	ms   int
	errs int // in a hundred
}{{"Read", true, 100, 0}, {"Read", true, 120, 0}, {"Edit", true, 200, 3}, {"Bash", false, 4100, 9}, {"Grep", true, 150, 0},
	{"TodoWrite", false, 40, 0}, {"Glob", true, 60, 0}, {"Write", true, 300, 0}, {"Task", false, 130_000, 11}, {"WebFetch", false, 9_800, 50}}

// dayLog is a day's requests, the Claude log's records or the worker's events for Codex.
func dayLog(rnd *rand.Rand, fx fxSession, dirs map[string]string, day time.Time, events bool) []string {
	at := time.Date(day.Year(), day.Month(), day.Day(), 9+rnd.Intn(3), rnd.Intn(60), 0, 0, time.UTC)
	if at.After(statsNow) {
		at = statsNow.Add(-3 * time.Hour)
	}
	ctx := int64(30_000)
	var out []string
	n := fx.perDay/2 + rnd.Intn(fx.perDay)
	for k := 0; k < n; k++ {
		if k%8 == 0 {
			out = append(out, prompt(at, events))
		}
		ctx += int64(rnd.Intn(3000))
		out = append(out, request(rnd, fx, dirs, at, ctx, k, events)...)
		at = at.Add(time.Duration(40+rnd.Intn(200)) * time.Second)
		if at.After(statsNow) {
			break
		}
	}
	return out
}

// roundingLog is pricing-rounding's morning: 09:12 to 12:17 with two idle stretches and a compaction.
func roundingLog(rnd *rand.Rand, dirs map[string]string) []string {
	fx := fxSessions[len(fxSessions)-1]
	at := time.Date(2026, 10, 14, 9, 12, 0, 0, time.UTC)
	ctx := int64(40_000)
	var out []string
	k := 0
	for at.Before(statsNow) {
		switch {
		case at.Hour() == 10 && at.Minute() >= 20 && at.Minute() < 46:
			at = time.Date(2026, 10, 14, 10, 46, 0, 0, time.UTC)
		case at.Hour() == 11 && at.Minute() >= 22 && at.Minute() < 38:
			at = time.Date(2026, 10, 14, 11, 38, 0, 0, time.UTC)
		}
		if k%11 == 0 {
			out = append(out, prompt(at, false))
		}
		ctx += int64(800 + rnd.Intn(1200))
		if ctx > 168_000 {
			ctx = 41_000
		}
		out = append(out, request(rnd, fx, dirs, at, ctx, k, false)...)
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

func prompt(at time.Time, events bool) string {
	if events {
		return fmt.Sprintf(`{"at":%q,"kind":"turn"}`, at.Format(time.RFC3339))
	}
	return fmt.Sprintf(`{"type":"user","timestamp":%q,"message":{"role":"user","content":"carry on"}}`, at.Format(time.RFC3339))
}

// request is one request with a tool call or two, and their results.
func request(rnd *rand.Rand, fx fxSession, dirs map[string]string, at time.Time, ctx int64, k int, events bool) []string {
	pick := func() string {
		total := 0
		for _, w := range fx.weights {
			total += w
		}
		n := rnd.Intn(total)
		for i, w := range fx.weights {
			if n < w {
				return dirs[fx.repos[i]]
			}
			n -= w
		}
		return dirs[fx.repos[0]]
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
			at.Add(2*time.Second).Format(time.RFC3339), fx.model, in+write, out*2, ctx*2))
	}
	effort := []string{"high", "high", "high", "medium", "low"}[rnd.Intn(5)]
	line := fmt.Sprintf(`{"type":"assistant","timestamp":%q,"effort":%q,"message":{"id":"m%d","model":%q,"content":[%s],`+
		`"usage":{"input_tokens":%d,"output_tokens":%d,"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		at.Format(time.RFC3339), effort, k, fx.model, strings.Join(calls, ","), in, out, ctx, write, write*2/3, write/3)
	return append([]string{line}, results...)
}

// readings is a week of plan readings, ending now: Claude's 5-hour, weekly and weekly Opus windows, and Codex's.
func (r *statsRig) readings(t *testing.T) {
	t.Helper()
	claude := r.claude.WaitStarted(t)
	codex := r.codex.WaitStarted(t)
	weekReset := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	at := statsNow.Add(-7 * 24 * time.Hour)
	for ; !at.After(statsNow); at = at.Add(time.Hour) {
		reset := weekReset
		if at.Before(weekReset.Add(-week)) {
			reset = weekReset.Add(-week)
		}
		opened := reset.Add(-week)
		h := at.Sub(opened).Hours()
		dayAgo := statsNow.Add(-24 * time.Hour)
		weekly := h * .0036
		switch {
		case reset == weekReset && at.After(dayAgo):
			weekly = .446 + at.Sub(dayAgo).Hours()*.011
		case reset == weekReset:
			weekly = .446 * h / dayAgo.Sub(opened).Hours()
		}
		fiveReset := opened.Add(time.Duration(int(at.Sub(opened).Hours()/5)+1) * 5 * time.Hour)
		five := .1 + .3*float64(int(at.Sub(opened).Hours())%5)/5*float64(1+int(at.Hour())%3)/3
		if at.Equal(statsNow.Truncate(time.Hour)) || at.After(statsNow.Add(-time.Hour)) {
			fiveReset, five = time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), .31
		}
		claude.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "claude", Account: workAccount, AccountName: "ada@acme.example",
			Plan: "Claude Max", Sampled: at, Source: "/usage, through the Oriel mod",
			Windows: []core.UsageWindow{
				{Name: "Current session", Used: five, Resets: fiveReset, Length: 5 * time.Hour},
				{Name: "Current week (all models)", Used: min(weekly, 1), Resets: reset, Length: week},
				{Name: "Current week (Opus)", Used: min(weekly*.82, 1), Resets: reset, Length: week},
			}}})
		codex.Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "codex", Account: "ada@example.com", AccountName: "ada@example.com",
			Plan: "ChatGPT Pro", Sampled: at, Source: "Codex's account/rateLimits/read",
			Windows: []core.UsageWindow{
				{Name: "5h", Used: .22 * at.Sub(statsNow.Add(-7*24*time.Hour)).Hours() / (7 * 24), Resets: time.Date(2026, 10, 14, 15, 27, 0, 0, time.UTC),
					Length: 5 * time.Hour},
				{Name: "weekly", Used: .48 * at.Sub(statsNow.Add(-7*24*time.Hour)).Hours() / (7 * 24), Resets: time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC),
					Length: week},
			}}})
	}
	last := func(u core.ProviderUsage) {
		claude.Emit(agent.UsageUpdate{Usage: u})
	}
	last(core.ProviderUsage{Provider: "claude", Account: workAccount, AccountName: "ada@acme.example", Plan: "Claude Max",
		Sampled: statsNow.Add(-30 * time.Minute), Source: "/usage, through the Oriel mod",
		Windows: []core.UsageWindow{
			{Name: "Current session", Used: .31, Resets: time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), Length: 5 * time.Hour},
			{Name: "Current week (all models)", Used: .705, Resets: weekReset, Length: week},
			{Name: "Current week (Opus)", Used: .575, Resets: weekReset, Length: week},
		}})
	last(core.ProviderUsage{Provider: "claude", Account: workAccount, AccountName: "ada@acme.example", Plan: "Claude Max",
		Sampled: statsNow.Add(-40 * time.Second), Source: "/usage, through the Oriel mod",
		Windows: []core.UsageWindow{
			{Name: "Current session", Used: .38, Resets: time.Date(2026, 10, 14, 14, 40, 0, 0, time.UTC), Length: 5 * time.Hour},
			{Name: "Current week (all models)", Used: .71, Resets: weekReset, Length: week},
			{Name: "Current week (Opus)", Used: .58, Resets: weekReset, Length: week},
		}})
}

// workAccount is the fixture's Claude login: the account and organisation of a company subscription.
const workAccount = "u1/o-acme"

// statsScene is the app on the rig's worker, on mockup 43's clock, with pricing-rounding in front.
func statsScene(t *testing.T) (*errScene, *statsRig, *statsExt) {
	t.Helper()
	r := newStatsRig(t)
	r.build()
	r.readings(t)
	s := newSceneWith(t, "", "", mockupSize, r.connect(t))
	s.now = statsNow
	var x *statsExt
	for _, e := range s.exts {
		if v, ok := e.(*statsExt); ok {
			x = v
		}
	}
	x.now, x.loc = func() time.Time { return s.now }, time.UTC
	waitState(t, s.store, "the sessions and readings", func(st core.State) bool {
		return len(st.Workers) == 1 && len(st.Sessions) == len(fxSessions) && len(st.Usage) == 2 && st.Usage[0].Sampled.After(statsNow.Add(-time.Minute))
	})
	if err := s.store.Apply(core.SessionFocused{ID: r.ids["pricing-rounding"]}); err != nil {
		t.Fatal(err)
	}
	if err := s.reg.Options().Set(AccountsOption, registry.LayerUser, registry.At{}, map[string]string{workAccount: "Work"}); err != nil {
		t.Fatal(err)
	}
	return s, r, x
}

// settle draws frames until every answer the page asked for is in.
func (s *errScene) settleStats(x *statsExt) {
	s.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		s.frame()
		s.sh.runTodo()
		x.mu.Lock()
		busy := len(x.inflight) > 0
		x.mu.Unlock()
		if !busy {
			s.frame()
			s.sh.runTodo()
			x.mu.Lock()
			busy = len(x.inflight) > 0
			x.mu.Unlock()
			if !busy {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.t.Fatal("the stats never came in")
}

func (s *errScene) wantFrame(t *testing.T, wants ...string) {
	t.Helper()
	text := s.frameText()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("the frame lacks %q:\n%s", w, text)
		}
	}
}

// AI-12: the global page grouped by repo (43's panel 2): the tiles, the chart in the repos' colours, the table with
// the split's repo-sessions; drilling into a repo adds its chip and regroups by session (panel 3).
func TestStatsPageSnapshots(t *testing.T) {
	s, _, x := statsScene(t)
	s.keyCommand(t, "<D-S>")
	s.settleStats(x)
	if !x.open {
		t.Fatal("⌘⇧S didn't open the Stats page")
	}
	s.keyCommand(t, "j")
	s.keyCommand(t, "j")
	s.settleStats(x)
	s.lint("the stats page")
	s.golden("stats-page")
	s.wantFrame(t, "STATS", "COST · EST.", "COST PER REPO PER DAY", "pricing", "billing-ui", "repo-sessions from", "TOKENS BY KIND",
		"TOOLS BY ERROR RATE", "HOURS OF THE DAY", "drill into")
	r, _ := x.report(x.query())
	want := r.Groups[x.sel].Key
	s.keyCommand(t, "<CR>")
	s.settleStats(x)
	if x.q.group != stats.BySession || len(x.q.filters["repo"]) != 1 || x.q.filters["repo"][0] != want {
		t.Errorf("drilling into %s: group %s, filters %v", want, x.q.group, x.q.filters)
	}
	s.keyCommand(t, "<BS>")
	if x.q.group != stats.ByRepo || len(x.q.filters) != 0 {
		t.Errorf("⌫ didn't back out: %s %v", x.q.group, x.q.filters)
	}
	s.keyCommand(t, "gs")
	s.settleStats(x)
	if x.q.group != stats.BySession {
		t.Fatalf("g s grouped by %s", x.q.group)
	}
	s.golden("stats-sessions")
	s.wantFrame(t, "COST PER SESSION PER DAY", "pricing-rounding", "usage-api", "span several repos")
}

// AI-12: one session's stats as a main tab (43's panel 1), from the session stats box's cost.
func TestSessionStatsSnapshot(t *testing.T) {
	s, r, x := statsScene(t)
	s.run("agent.console", nil)
	s.frame()
	s.frame()
	s.click(centre(s.access("Open the session's stats").Bounds))
	s.settleStats(x)
	if !s.showing(StatsSessionView) {
		t.Fatal("the session stats box's cost didn't open the session's stats")
	}
	s.run("view.close", registry.Args{"id": AgentConsoleView})
	s.settleStats(x)
	s.golden("stats-session")
	s.wantFrame(t, "pricing-rounding", "COST OVER THE SESSION", "COST PER REPO", "count whole session in each", "MODELS", "TOOLS",
		"compacted", "idle", "turns", "EFFORT")
	d, _ := x.detail(r.ids["pricing-rounding"])
	if len(d.Repos) != 2 || d.Repos[0].Repo != "pricing" || d.Repos[0].Share < .6 || d.Repos[0].Share > .76 {
		t.Errorf("pricing-rounding's repos = %+v", d.Repos)
	}
	// The toggle and the setting agree: counted whole, each repo is charged the session.
	s.click(centre(s.access("Count whole session in each").Bounds))
	s.settleStats(x)
	if x.split() {
		t.Error("the session tab's toggle didn't turn stats.split_sessions off")
	}
	d, _ = x.detail(r.ids["pricing-rounding"])
	if len(d.Repos) != 2 || d.Repos[0].Cost != d.Repos[1].Cost {
		t.Errorf("counted whole, repos = %+v", d.Repos)
	}
	s.keyCommand(t, "<D-S>")
	s.settleStats(x)
	if on := s.access("Split multi-repo sessions").Selected; on {
		t.Error("the page's toggle doesn't show the setting off")
	}
}

// AI-12: Usage (43's panel 5): each window's burn and where it takes it; the weekly one runs out before its reset.
func TestUsageSnapshot(t *testing.T) {
	s, _, x := statsScene(t)
	s.keyCommand(t, "<D-U>")
	s.settleStats(x)
	if !x.usageOpen {
		t.Fatal("⌘⇧U didn't open Usage")
	}
	s.lint("the usage page")
	s.golden("usage")
	s.wantFrame(t, "USAGE", "Claude Max", "Work", "5-HOUR WINDOW", "WEEKLY", "WEEKLY · OPUS", "BURNED THE WEEKLY WINDOW",
		"won't run out", "before the reset", "WINDOW USAGE · LAST 7 DAYS", "WORK · CLAUDE MAX · MBP")
	// Codex's account is its own: picked, its windows show instead.
	s.click(s.textPoint(t, "ADA@EXAMPLE.COM · CHATGPT PRO · MBP"))
	s.settleStats(x)
	s.wantFrame(t, "ChatGPT Pro", "22%", "48%")
}

// AI-12: the projection from fixture readings: 71% used, 1.1%/h over the last 24h, resetting in 44h 43m: it runs out
// after (1-.71)/.011 = 26.4 hours, Thu 14:38, 18h 21m before the reset.
func TestUsageProjection(t *testing.T) {
	reset := time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	w := core.UsageWindow{Name: "weekly", Used: .71, Resets: reset, Length: 7 * 24 * time.Hour}
	hist := []core.ProviderUsage{
		{Sampled: statsNow.Add(-48 * time.Hour), Windows: []core.UsageWindow{{Name: "weekly", Used: .30, Resets: reset}}},
		{Sampled: statsNow.Add(-24 * time.Hour), Windows: []core.UsageWindow{{Name: "weekly", Used: .446, Resets: reset}}},
		{Sampled: statsNow, Windows: []core.UsageWindow{w}},
	}
	r := readWindow(statsNow, w, hist)
	if r.burn < .0109 || r.burn > .0111 {
		t.Errorf("burn = %.4f/h, want .011", r.burn)
	}
	out := statsNow.Add(time.Duration((1 - .71) / r.burn * float64(time.Hour)))
	if absDur(r.runsOut.Sub(out)) > time.Second || r.runsOut.Format("Mon 15:04") != "Thu 14:38" {
		t.Errorf("runs out %v, want %v", r.runsOut, out)
	}
	v, _ := r.verdict(statsNow, time.UTC)
	if v != "at this rate the weekly window runs out Thu 14:38 · 18h 21m before the reset" {
		t.Errorf("verdict = %q", v)
	}
	five := core.UsageWindow{Name: "5h", Used: .38, Resets: statsNow.Add(2*time.Hour + 23*time.Minute), Length: 5 * time.Hour}
	r = readWindow(statsNow, five, []core.ProviderUsage{
		{Sampled: statsNow.Add(-30 * time.Minute), Windows: []core.UsageWindow{{Name: "5h", Used: .31, Resets: five.Resets}}},
	})
	if v, _ := r.verdict(statsNow, time.UTC); r.burn < .139 || r.burn > .141 || v != "≈ 71% at the reset · won't run out" {
		t.Errorf("5h: burn %.3f, %q", r.burn, v)
	}
}

// A window's points go to the repos that used tokens while it rose; a rise with none behind it is outside Oriel.
func TestBurnedSpreadsPointsByTokens(t *testing.T) {
	opened := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	w := core.UsageWindow{Used: .30, Resets: opened.Add(5 * time.Hour), Length: 5 * time.Hour}
	r := stats.Report{Points: []time.Time{opened, opened.Add(time.Hour), opened.Add(2 * time.Hour)}, Groups: []stats.Group{
		{Key: "api", Used: []int64{300, 0, 0}}, {Key: "web", Used: []int64{100, 0, 0}},
	}}
	rows, outside := burned(r, w, []reading{{opened.Add(time.Hour), .2}, {opened.Add(150 * time.Minute), .3}})
	if math.Abs(rows[0].points-15) > 1e-9 || math.Abs(rows[1].points-5) > 1e-9 || math.Abs(outside-10) > 1e-9 {
		t.Errorf("rows %+v, outside %v; want api 15, web 5, 10 outside", rows, outside)
	}
}

// AI-12: every way to the pages opens them: the sidebar's entries, the title bar's meters, :Stats and :Usage, the
// keys, and the start view's links.
func TestStatsEntryPoints(t *testing.T) {
	s, _, x := statsScene(t)
	s.settleStats(x)
	s.wantFrame(t, "today", "wk 71%")
	s.click(centre(s.access("Stats").Bounds))
	if !x.open {
		t.Error("the sidebar's Stats didn't open the page")
	}
	x.open = false
	s.frame()
	s.click(centre(s.access("Usage").Bounds))
	if !x.usageOpen {
		t.Error("the sidebar's Usage didn't open the page")
	}
	x.usageOpen = false
	s.frame()
	s.click(centre(s.access("Claude Max usage").Bounds))
	if !x.usageOpen {
		t.Error("the title bar's meters didn't open Usage")
	}
	s.keyCommand(t, "<Esc>")
	if x.usageOpen {
		t.Error("Escape didn't close Usage")
	}
	s.run("Stats", registry.Args{"args": "repo=api 7d"})
	if !x.open || x.q.rng != 0 || x.q.filters["repo"][0] != "api" {
		t.Errorf(":Stats repo=api 7d: open %v, range %d, filters %v", x.open, x.q.rng, x.q.filters)
	}
	s.run("Usage", nil)
	if !x.usageOpen || x.open {
		t.Error(":Usage didn't open Usage in Stats' place")
	}
	s.run("Stats", registry.Args{"args": "session"})
	s.frame()
	if !s.showing(StatsSessionView) {
		t.Error(":Stats session didn't open the session's tab")
	}
	// The pages are in the omnibox, with their keys.
	var labels []string
	for _, src := range s.reg.Pickers() {
		if src.ID == "pages" {
			for _, it := range src.Items(context.Background(), "") {
				labels = append(labels, it.Label+" "+it.Key)
			}
		}
	}
	if got := strings.Join(labels, ", "); !strings.Contains(got, "Stats ⇧⌘S") || !strings.Contains(got, "Usage ⇧⌘U") ||
		!strings.Contains(got, "Settings ⌘,") {
		t.Errorf("pages = %s", got)
	}
}

// AI-12: the start view's links, each with its figure, open their pages.
func TestStartViewLinks(t *testing.T) {
	r := newStatsRig(t)
	s := newSceneWith(t, "", "", mockupSize, r.connect(t))
	var x *statsExt
	for _, e := range s.exts {
		if v, ok := e.(*statsExt); ok {
			x = v
		}
	}
	waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	s.settleStats(x)
	s.click(centre(s.startCard("Stats").Bounds))
	if !x.open {
		t.Error("the start view's Stats link didn't open the page")
	}
	x.open = false
	s.frame()
	s.click(centre(s.startCard("Usage").Bounds))
	if !x.usageOpen {
		t.Error("the start view's Usage link didn't open the page")
	}
}

// AI-12: plan usage belongs to an account. Three workers: a work laptop and a devbox on the company's Claude account,
// a personal laptop on its own. The shared account is one plan, reported by both its workers; the other is separate,
// never added to it. Each session's meters are its own account's.
func TestUsageIsPerAccount(t *testing.T) {
	type machine struct {
		id      core.WorkerID
		account string
		used    float64
	}
	machines := []machine{{"mbp-work", "u1/o-acme", .71}, {"mbp-home", "u1/o-personal", .22}, {"devbox", "u1/o-acme", .71}}
	var changes []func(*Config)
	var sessions []core.SessionID
	for _, m := range machines {
		fake := agenttest.NewFake()
		fake.Name = "claude"
		lw := newWorkerWith(t, worker.Options{ID: m.id, Name: string(m.id)}, fake)
		id, err := lw.w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "on-" + string(m.id), Prompt: "go",
			Provider: "claude", Repos: []core.RepoID{core.RepoID(lw.repo)}})
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, id)
		fake.WaitStarted(t).Emit(agent.UsageUpdate{Usage: core.ProviderUsage{Provider: "claude", Account: m.account,
			AccountName: strings.TrimPrefix(m.account, "u1/o-") + "@example.com", Plan: "Claude Max", Sampled: statsNow,
			Windows: []core.UsageWindow{{Name: "Current week (all models)", Used: m.used, Resets: statsNow.Add(40 * time.Hour), Length: 7 * 24 * time.Hour}}}})
		changes = append(changes, lw.connect(t))
	}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		for _, c := range changes {
			c(cfg)
		}
	})
	s.now = statsNow
	st := waitState(t, s.store, "two accounts, the shared one from both its workers", func(st core.State) bool {
		if len(st.Usage) != 2 || len(st.Workers) != 3 {
			return false
		}
		for _, u := range st.Usage {
			if u.Account == "u1/o-acme" && len(u.Workers) == 2 {
				return true
			}
		}
		return false
	})
	var x *statsExt
	for _, e := range s.exts {
		if v, ok := e.(*statsExt); ok {
			x = v
		}
	}
	x.now, x.loc = func() time.Time { return s.now }, time.UTC
	for _, u := range st.Usage {
		if u.Account == "u1/o-personal" && (len(u.Workers) != 1 || u.Workers[0] != "mbp-home" || u.Windows[0].Used != .22) {
			t.Errorf("the personal account = %+v", u)
		}
	}
	for i, m := range machines {
		s.store.Apply(core.SessionFocused{ID: sessions[i]})
		if u, _ := x.frontUsage(); u.Account != m.account {
			t.Errorf("the session on %s shows %s's plan, want %s's", m.id, u.Account, m.account)
		}
	}
	s.run("usage.open", nil)
	s.settleStats(x)
	s.wantFrame(t, "ACCOUNT", "ACME@EXAMPLE.COM · CLAUDE MAX · DEVBOX, MBP-WORK", "PERSONAL@EXAMPLE.COM · CLAUDE MAX · MBP-HOME", "71%")
	s.click(s.textPoint(t, "PERSONAL@EXAMPLE.COM · CLAUDE MAX · MBP-HOME"))
	s.settleStats(x)
	s.wantFrame(t, "22%")
	if u, _ := x.usageAccount(); u.Account != "u1/o-personal" {
		t.Errorf("the switcher picked %s", u.Account)
	}
}
