package rig

import (
	"context"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/internal/isolate"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/gitx/gittest"
)

// FakeSandboxes run a sandboxed agent the way the fake provider runs any: on nothing, offering the backends Offered.
type FakeSandboxes struct{ Offered []string }

func (FakeSandboxes) Open(context.Context, core.SessionState, host.Approve, host.SandboxAgent) (host.Sandbox, error) {
	return FakeSandbox{}, nil
}

func (f FakeSandboxes) Backends() []string { return f.Offered }

func (FakeSandboxes) Sweep(context.Context, func(string) bool, func(string) bool) []string {
	return nil
}

// FakeSandbox is a sandbox that runs nothing.
type FakeSandbox struct{}

func (FakeSandbox) Exec() agent.Exec     { return NopExec{} }
func (FakeSandbox) Spec() *agent.Sandbox { return &agent.Sandbox{} }
func (FakeSandbox) Close() error         { return nil }

// Clock is the session host's clock, which a test moves.
type Clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// Jobs is a fixture machine, mbp: api, web and pricing, six jobs, and their runs with transcripts to cost them.
type Jobs struct {
	T      *testing.T
	W      *host.Host
	Claude *LoggedFake
	Clock  *Clock
	Repos  map[string]string
}

// NewJobs is the rig, its session host offering sandboxes in those backends.
func NewJobs(t *testing.T, sandboxes ...string) *Jobs {
	t.Helper()
	isolate.Test(t)
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	r := &Jobs{T: t, Claude: NewLoggedFake(), Clock: &Clock{t: StatsNow},
		Repos: map[string]string{}}
	r.Claude.Name = "claude"
	root := t.TempDir()
	for _, name := range []string{"api", "web", "pricing"} {
		g := &gittest.Repo{T: t, Dir: filepath.Join(root, name), When: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)}
		os.MkdirAll(g.Dir, 0o755)
		g.Git("init", "-q", "-b", "main")
		g.Write(map[string]string{"README": name + "\n"})
		g.Commit("Ada", "first")
		r.Repos[name] = g.Dir
	}
	providers := &agent.Providers{}
	providers.Register(r.Claude)
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: providers, Exec: NopExec{}, Load: SteadyLoad, Now: r.Clock.Now,
		Sandbox: FakeSandboxes{sandboxes}, Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.W = w
	ctx := context.Background()
	for _, name := range []string{"api", "web", "pricing"} {
		if _, err := w.AddRepoPath(ctx, r.Repos[name]); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

// Save saves j as if at at.
func (r *Jobs) Save(at time.Time, j host.Job) host.Job {
	r.T.Helper()
	r.Clock.Set(at)
	saved, err := r.W.SaveJob(j)
	if err != nil {
		r.T.Fatal(err)
	}
	return saved
}

// FxRun is a run as it went: when, for how long, in which repos (and how much of its work each), and how it ended.
type FxRun struct {
	Start   time.Time
	Took    time.Duration
	Repos   []string
	Weights []int
	Status  string
	Summary string
}

// Run runs job as fx went.
func (r *Jobs) Run(job string, fx FxRun) core.SessionID {
	t := r.T
	t.Helper()
	ctx := context.Background()
	r.Clock.Set(fx.Start)
	id, err := r.W.RunJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	a := r.Claude.WaitStarted(t)
	for _, name := range fx.Repos {
		if err := r.W.AddRepo(ctx, id, core.RepoID(r.Repos[name])); err != nil {
			t.Fatal(err)
		}
	}
	se, _ := r.W.Store().State().Session(id)
	dirs := map[string]string{}
	for _, p := range se.Snapshot().Projects {
		dirs[p.Name] = p.Dir
	}
	session := FxSession{Model: "claude-opus-4-7", Repos: fx.Repos, Weights: fx.Weights}
	rnd := rand.New(rand.NewSource(fx.Start.Unix()))
	var lines []string
	n := int(fx.Took.Minutes()) * 3
	for k := 0; k < n; k++ {
		at := fx.Start.Add(fx.Took * time.Duration(k) / time.Duration(n))
		if k%8 == 0 {
			lines = append(lines, Prompt(at, false))
		}
		lines = append(lines, Request(rnd, session, dirs, at, int64(30_000+k*900), k, false)...)
	}
	if len(lines) > 0 {
		p := filepath.Join(t.TempDir(), string(id)+".jsonl")
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		r.Claude.SetLog(se.Snapshot().Dir, p)
	}
	r.Clock.Set(fx.Start.Add(fx.Took))
	tid := string(se.Snapshot().Threads[0].ID)
	switch fx.Status {
	case host.RunDone:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.Summary, "resolve": true})
		r.Stop(id)
	case host.RunBlocked:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": "**Blocked:** " + fx.Summary})
		r.Stop(id)
	case host.RunRunning:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.Summary, "working": true})
	case host.RunStopped:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.Summary, "working": true})
		r.Stop(id)
	}
	return id
}

// Stop stops a run's agent, as a finished run's is once Oriel starts again.
func (r *Jobs) Stop(id core.SessionID) {
	r.T.Helper()
	if err := r.W.Stop(context.Background(), id); err != nil {
		r.T.Fatal(err)
	}
}

// Day44 is a time in the jobs fixture's year.
func Day44(month time.Month, d, h, m int) time.Time {
	return time.Date(2026, month, d, h, m, 0, 0, time.UTC)
}

// Build is the six jobs and their runs.
func (r *Jobs) Build() {
	sweep := "In api, web and pricing, bump every dependency that's behind on a minor or patch version. Keep major versions where they are.\n" +
		"Run each repo's tests after bumping. If one bump breaks them, back that bump out and say why in the summary; don't fix code to fit it.\n" +
		"Open one PR per repo titled `chore(deps): sweep`, linked to each other. Stop as blocked if a repo won't build before you start."
	opus := host.Job{Provider: "claude", Model: "claude-opus-4-7", Effort: "high"}
	job := func(at time.Time, title, prompt, pin string, s host.Schedule) string {
		j := opus
		j.Title, j.Prompt, j.Sandbox, j.Schedule = title, prompt, pin, s
		return r.Save(at, j).Name
	}
	changelog := job(Day44(10, 12, 9, 0), "Weekly changelog draft", "Draft this week's changelog for api and web from the PRs merged since last Monday.", "",
		host.Schedule{})
	_ = changelog
	pts := job(Day44(10, 1, 9, 0), "Pricing test sweep", "In pricing, find the slowest tests and say why they're slow.", "sandbox", host.Schedule{})
	feed := job(Day44(9, 20, 9, 0), "Price-feed backfill", "Backfill the price feed in pricing for the days the importer missed.", "host", host.Schedule{})
	flaky := job(Day44(9, 12, 9, 0), "Triage flaky e2e on main", "Find which web e2e tests flaked on main this week, and why.", "sandbox", host.Schedule{})
	ci := job(Day44(9, 5, 9, 0), "Morning CI triage on my open PRs", "For each of my open PRs in api and web, say what CI failed and why.", "sandbox",
		host.Schedule{Every: host.EveryWeekday, At: "07:00"})
	deps := job(Day44(8, 20, 9, 0), "Sweep stale deps, keep major versions", sweep, "sandbox", host.Schedule{})
	all := []string{"api", "web", "pricing"}
	for _, fx := range []FxRun{
		{Day44(8, 26, 7, 0), 24 * time.Minute, all, []int{3, 2, 1}, host.RunDone, "3 PRs · 22 bumps"},
		{Day44(9, 2, 7, 0), 21 * time.Minute, all, []int{3, 2, 2}, host.RunDone, "3 PRs · 30 bumps"},
		{Day44(9, 9, 7, 0), 19 * time.Minute, all, []int{4, 2, 2}, host.RunDone, "3 PRs · 27 bumps · one bump backed out (grpc 1.66)"},
		{Day44(9, 16, 9, 12), 3 * time.Minute, []string{"api"}, []int{1}, host.RunStopped, "Listing what's behind in api."},
		{Day44(9, 23, 7, 0), 18 * time.Minute, []string{"api", "pricing"}, []int{3, 2}, host.RunDone, "2 PRs · 18 bumps · web already current"},
		{Day44(9, 30, 7, 0), 9 * time.Minute, []string{"web"}, []int{1}, host.RunBlocked,
			"react-router 6.28 breaks loaders in 3 routes; held at 6.27. Needs a call: bump and fix, or pin?"},
		{Day44(10, 7, 7, 0), 22 * time.Minute, all, []int{5, 4, 3}, host.RunDone, "3 PRs: api#812, web#4431, pricing#212 · 31 bumps · all green"},
		{Day44(10, 14, 12, 13), 4 * time.Minute, []string{"api", "web"}, []int{5, 1}, host.RunRunning,
			"Bumping 14 deps in api; go test running. web next, pricing not added yet."},
	} {
		r.Run(deps, fx)
	}
	r.Run(ci, FxRun{Day44(10, 14, 7, 0), 14 * time.Minute, []string{"api", "web"}, []int{1, 1}, host.RunDone, "No failing CI on your 4 open PRs."})
	r.Run(flaky, FxRun{Day44(10, 13, 16, 40), 11 * time.Minute, []string{"web"}, []int{1}, host.RunBlocked,
		"Two flaky specs share a fixture; fix the fixture or quarantine them?"})
	r.Run(feed, FxRun{Day44(10, 13, 11, 2), 16 * time.Minute, []string{"pricing"}, []int{1}, host.RunDone, "Backfilled Oct 3–9: 7 days, 12,400 rows."})
	r.Run(pts, FxRun{Day44(10, 11, 9, 0), 6 * time.Minute, []string{"pricing"}, []int{1}, host.RunStopped, "Timing the suite."})
	r.Clock.Set(StatsNow)
}
