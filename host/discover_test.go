package host

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/codex"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/stats"
)

// claudeLike is a fake that keeps and finds its logs where Claude Code does.
type claudeLike struct{ *agenttest.Fake }

func (claudeLike) Logs(cwd, session string) []agent.Log { return claude.Provider{}.Logs(cwd, session) }
func (claudeLike) Discover() []agent.Transcript         { return claude.Provider{}.Discover() }

// codexLike is a fake that finds its rollouts where Codex keeps them, and keeps no log the host reads: its sessions'
// events are the host's record.
type codexLike struct{ *agenttest.Fake }

func (codexLike) Discover() []agent.Transcript { return (&codex.Provider{}).Discover() }

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claudeLog is a Claude Code log of a prompt in cwd and a $5 answer of opus output.
func claudeLog(at time.Time, cwd, prompt string) []string {
	ts := at.UTC().Format(time.RFC3339)
	return []string{
		fmt.Sprintf(`{"type":"user","timestamp":%q,"cwd":%q,"message":{"role":"user","content":[{"type":"text","text":%q}]}}`, ts, cwd, prompt),
		fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":%q,"message":{"id":"m-%s","model":"claude-opus-5","content":[],"usage":{"input_tokens":0,"output_tokens":200000}}}`,
			at.Add(time.Second).UTC().Format(time.RFC3339), cwd, ts),
	}
}

// codexRollout is a Codex rollout in cwd: a prompt, and one response of 1M fresh input tokens.
func codexRollout(at time.Time, cwd, prompt string) []string {
	ts := at.UTC().Format("2006-01-02T15:04:05.000Z")
	return []string{
		fmt.Sprintf(`{"timestamp":%q,"type":"session_meta","payload":{"id":"x","timestamp":%q,"cwd":%q}}`, ts, ts, cwd),
		fmt.Sprintf(`{"timestamp":%q,"type":"turn_context","payload":{"cwd":%q,"model":"gpt-5-codex","effort":"high"}}`, ts, cwd),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"user_message","message":%q}}`, ts, prompt),
		fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":1000000,"cached_input_tokens":0,"output_tokens":0,"total_tokens":1000000},"model_context_window":272000}}}`, ts),
	}
}

func rolloutPath(home string, at time.Time, thread string) string {
	return filepath.Join(home, "sessions", at.Format("2006/01/02"), "rollout-"+at.Format("2006-01-02T15-04-05")+"-"+thread+".jsonl")
}

// The stats read every transcript Claude and Codex keep on the machine: an Oriel session's own counts once, as
// Oriel's, and one no Oriel session owns counts as outside Oriel, named from its first prompt, its repo from its cwd.
// Claude's are found under $CLAUDE_CONFIG_DIR.
func TestStatsReadEveryTranscript(t *testing.T) {
	config, codexHome := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	t.Setenv("CODEX_HOME", codexHome)
	cl, cx := claudeLike{agenttest.NewFake()}, codexLike{agenttest.NewFake()}
	cl.Name, cx.Name = "claude", "codex"
	r := newRig(t, cl, cx)
	at := r.now.Add(-24 * time.Hour)

	in := r.start(core.SessionSpec{Kind: core.KindWork, Name: "pricing-rounding", Provider: "claude", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	cl.WaitStarted(t)
	se := r.eventually(in, "its agent's id", func(se core.SessionState) bool { return se.Agent.Session != "" })
	writeLines(t, transcript.Path(config, agentDir(se), se.Agent.Session), claudeLog(at, agentDir(se), "round half to even")...)

	cin := r.start(core.SessionSpec{Kind: core.KindWork, Name: "search-api", Provider: "codex", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	cs := cx.WaitStarted(t)
	cse := r.eventually(cin, "its agent's id", func(se core.SessionState) bool { return se.Agent.Session != "" })
	cs.Emit(agent.TurnStart{})
	cs.Emit(agent.TurnEnd{Usage: agent.Usage{Input: 10, Output: 20}})
	r.eventually(cin, "the turn's end", func(se core.SessionState) bool { return se.Agent.Stats.Turns == 1 })
	writeLines(t, rolloutPath(codexHome, at, cse.Agent.Session), codexRollout(at, agentDir(cse), "count the calls")...)

	web := namedRepo(t, "web")
	writeLines(t, filepath.Join(config, "projects", transcript.Slug(web), "stray.jsonl"), claudeLog(at, filepath.Join(web, "src"), "tidy the css\nand the html")...)
	elsewhere := filepath.Join(t.TempDir(), "scratch")
	writeLines(t, rolloutPath(codexHome, at, "0199a213-81c0-7800-8aa1-bbab2a035a53"), codexRollout(at, elsewhere, "")...)
	old := filepath.Join(config, "projects", "-w-old", "old.jsonl")
	writeLines(t, old, claudeLog(r.now.AddDate(0, 0, -200), "/w/old", "long ago")...)
	os.Chtimes(old, r.now.AddDate(0, 0, -200), r.now.AddDate(0, 0, -200))

	q := stats.Query{Group: stats.BySession, From: r.now.AddDate(0, 0, -7), To: r.now.Add(time.Hour)}
	rep := r.w.StatsQuery(q)
	got := map[string]stats.Group{}
	for _, g := range rep.Groups {
		got[g.Key] = g
	}
	if g := got[string(in)]; g.Origin != stats.OriginOriel || g.Requests != 1 || !near(g.Cost, 5) {
		t.Errorf("the Oriel Claude session = %+v, want its one request, as Oriel's", g)
	}
	if g := got[string(cin)]; g.Origin != stats.OriginOriel || g.Requests != 1 {
		t.Errorf("the Oriel Codex session = %+v, want its recorded request only", g)
	}
	stray := got["claude:stray"]
	if stray.Origin != stats.OriginOutside || stray.Label != "tidy the css" || len(stray.Repos) != 1 || stray.Repos[0] != "web" ||
		!near(stray.Cost, 5) {
		t.Errorf("the stray Claude log = %+v", stray)
	}
	cstray := got["codex:0199a213-81c0-7800-8aa1-bbab2a035a53"]
	if cstray.Origin != stats.OriginOutside || cstray.Label != "scratch" || len(cstray.Repos) != 1 || cstray.Repos[0] != "scratch" ||
		!near(cstray.Cost, 1.25) {
		t.Errorf("the stray Codex rollout = %+v", cstray)
	}
	if len(rep.Groups) != 4 || rep.Totals.Sessions != 4 {
		t.Errorf("sessions = %d in %d groups, want each transcript once", rep.Totals.Sessions, len(rep.Groups))
	}
	if len(rep.Origins) != 2 || rep.Origins[0].Sessions != 2 || rep.Origins[1].Sessions != 2 || !near(rep.Origins[1].Cost, 6.25) {
		t.Errorf("origins = %+v", rep.Origins)
	}
	if n := len(r.w.statsStore().Scans("claude:old")); n != 0 {
		t.Errorf("a log last written before the range was read: %d scans", n)
	}

	d, err := r.w.SessionStats(StatsSessionReq{Session: "claude:stray"})
	if err != nil || d.Origin != stats.OriginOutside || d.Name != "tidy the css" || d.Totals.Requests != 1 || d.Provider != "claude" {
		t.Errorf("an outside session's stats = %+v, %v", d, err)
	}
	if d, err := r.w.SessionStats(StatsSessionReq{Session: "claude:old"}); err != nil || d.Totals.Requests != 1 {
		t.Errorf("an old outside session's stats = %+v, %v", d, err)
	}
	if _, err := r.w.SessionStats(StatsSessionReq{Session: "claude:nothing"}); err == nil {
		t.Error("stats of a session there's no log of")
	}
}
