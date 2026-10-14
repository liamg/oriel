package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker/stats"
)

// loggedFake is the fake provider keeping a log of its own, as Claude does: the fixture transcript of the session
// started in each directory.
type loggedFake struct {
	*agenttest.Fake
	mu   sync.Mutex
	logs map[string]string // agent dir → transcript
}

func (f *loggedFake) Logs(cwd, _ string) []agent.Log {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p, ok := f.logs[cwd]; ok {
		return []agent.Log{{Path: p, Format: stats.FormatClaude}}
	}
	return nil
}

func namedRepo(t *testing.T, name string) string {
	t.Helper()
	r := &gittest.Repo{T: t, Dir: filepath.Join(t.TempDir(), name), When: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	os.MkdirAll(r.Dir, 0o755)
	r.Git("init", "-q", "-b", "main")
	r.Write(map[string]string{"README": name + "\n"})
	r.Commit("Ada", "first")
	return r.Dir
}

// transcript is Claude Code's log of a session that read files in each of dirs (n calls in dirs[i] for counts[i]),
// spent $5 of opus output on Oct 1 and $3 on Oct 2.
func fixtureLog(t *testing.T, dirs []string, counts []int) string {
	t.Helper()
	var calls []string
	k := 0
	for i, d := range dirs {
		for range counts[i] {
			k++
			calls = append(calls, fmt.Sprintf(`{"type":"tool_use","id":"c%d","name":"Read","input":{"file_path":%q}}`, k, d+"/README"))
		}
	}
	line := func(at, content string, out int) string {
		return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"message":{"id":%q,"model":"claude-opus-5","content":[%s],`+
			`"usage":{"input_tokens":0,"output_tokens":%d}}}`, at, at, content, out)
	}
	p := filepath.Join(t.TempDir(), "log.jsonl")
	body := line("2026-10-01T09:00:00Z", strings.Join(calls, ","), 200_000) + "\n" + line("2026-10-02T09:00:00Z", "", 120_000) + "\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func statsWorker(t *testing.T) (*Worker, *loggedFake) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	f := &loggedFake{Fake: agenttest.NewFake(), logs: map[string]string{}}
	providers := &agent.Providers{}
	providers.Register(f)
	w, err := New(Options{Home: t.TempDir(), CritHome: "-", Providers: providers, Exec: nopExec{},
		Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name}, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, f
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// AI-12: a worker with fixture transcripts for two multi-repo sessions answers stats.query grouped by repo with the
// split rule, and by session; a rescan reads only what was appended.
func TestStatsQueryGroupsByRepoAndSession(t *testing.T) {
	w, f := statsWorker(t)
	pricing, billing, api := namedRepo(t, "pricing"), namedRepo(t, "billing-ui"), namedRepo(t, "api")
	start := func(name string, repos ...string) core.SessionState {
		var ids []core.RepoID
		for _, r := range repos {
			ids = append(ids, core.RepoID(r))
		}
		id, err := w.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: name, Provider: "fake", Repos: ids})
		if err != nil {
			t.Fatal(err)
		}
		se, _ := w.session(id)
		return se
	}
	a := start("pricing-rounding", pricing, billing)
	b := start("usage-api", api, billing)
	// a: 3 file calls in pricing, 1 in billing-ui. b: 1 in api, 1 in billing-ui.
	f.logs[agentDir(a)] = fixtureLog(t, []string{a.Projects[0].Dir, a.Projects[1].Dir}, []int{3, 1})
	f.logs[agentDir(b)] = fixtureLog(t, []string{b.Projects[0].Dir, b.Projects[1].Dir}, []int{1, 1})

	lw, app := wire.Pipe()
	w.Serve(app)
	t.Cleanup(func() { lw.Close(); app.Close() })
	c := Client{Conn: lw}
	q := stats.Query{From: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC), Split: true}
	r, err := c.StatsQuery(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	cost := map[string]float64{}
	for _, g := range r.Groups {
		cost[g.Key] = g.Cost
	}
	// $8 each: a splits 6/2, b 4/4.
	if want := map[string]float64{"pricing": 6, "billing-ui": 6, "api": 4}; len(cost) != 3 || !near(cost["pricing"], want["pricing"]) ||
		!near(cost["billing-ui"], want["billing-ui"]) || !near(cost["api"], want["api"]) {
		t.Errorf("by repo = %v, want %v", cost, want)
	}
	if !near(r.Totals.Cost, 16) || r.Totals.Sessions != 2 || r.Totals.MultiRepo != 2 || r.Worker != string(core.LocalWorker) {
		t.Errorf("totals = %+v from %q", r.Totals, r.Worker)
	}
	q.Group = stats.BySession
	r, _ = c.StatsQuery(context.Background(), q)
	if len(r.Groups) != 2 || r.Groups[0].Label != "pricing-rounding" || !near(r.Groups[0].Cost, 8) || len(r.Groups[0].Repos) != 2 {
		t.Errorf("by session = %+v", r.Groups)
	}

	// A rescan reads only what was appended: the bytes already read are rewritten and don't count again.
	p := f.logs[agentDir(a)]
	body, _ := os.ReadFile(p)
	body = []byte(strings.Replace(string(body), `"output_tokens":120000`, `"output_tokens":999999`, 1))
	body = append(body, []byte(`{"type":"assistant","timestamp":"2026-10-03T09:00:00Z","message":{"id":"m3","model":"claude-opus-5","content":[],"usage":{"input_tokens":0,"output_tokens":40000}}}`+"\n")...)
	os.WriteFile(p, body, 0o644)
	q.Group = stats.ByRepo
	r, _ = c.StatsQuery(context.Background(), q)
	if !near(r.Totals.Cost, 17) {
		t.Errorf("after an append, total = %v, want $17: only the new $1 read", r.Totals.Cost)
	}

	d, err := c.SessionStats(context.Background(), StatsSessionReq{Session: a.ID, Split: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Repos) != 2 || d.Repos[0].Repo != "pricing" || !near(d.Repos[0].Share, .75) || d.Totals.Requests != 3 {
		t.Errorf("session stats = %+v", d)
	}
}

// A provider with no log of its own has its events recorded by the worker, which the stats read.
func TestStatsFromRecordedEvents(t *testing.T) {
	r := newRig(t)
	id, fs := r.workSession("Fix stale usage prices")
	se := r.session(id)
	p, _ := se.Primary()
	fs.Emit(agent.TurnStart{})
	fs.Emit(agent.ToolCall{ID: "e1", Tool: "Edit", Input: json.RawMessage(fmt.Sprintf(`{"file_path":%q}`, filepath.Join(p.Dir, "round.go")))})
	fs.Emit(agent.ToolResult{ID: "e1", IsError: true})
	fs.Emit(agent.TurnEnd{Usage: agent.Usage{Input: 10, Output: 20}})
	r.eventually(id, "the turn's end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	d, err := r.w.SessionStats(StatsSessionReq{Session: id, Split: true})
	if err != nil {
		t.Fatal(err)
	}
	if d.Totals.Turns != 1 || d.Totals.Requests != 1 || d.Totals.ToolErrors != 1 || len(d.Repos) != 1 || d.Repos[0].Files != 1 {
		t.Errorf("stats = %+v", d)
	}
}

// The plan readings a worker sees are kept for the Usage page's week of history.
func TestUsageHistory(t *testing.T) {
	r := newRig(t)
	for i, used := range []float64{.1, .2} {
		r.w.usageSampled(core.ProviderUsage{Provider: "claude", Sampled: r.now.Add(time.Duration(i) * time.Hour),
			Windows: []core.UsageWindow{{Name: "weekly", Used: used}}})
	}
	r.w.usageSampled(core.ProviderUsage{Provider: "codex", Sampled: r.now, Windows: []core.UsageWindow{{Name: "5h", Used: .5}}})
	h := r.w.UsageHistory(UsageHistoryReq{Provider: "claude"})
	if len(h) != 2 || h[1].Windows[0].Used != .2 {
		t.Errorf("history = %+v", h)
	}
	if len(r.w.store.State().Usage) != 2 {
		t.Errorf("state usage = %+v", r.w.store.State().Usage)
	}
}
