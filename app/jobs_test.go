package app

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/ui/widget"
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
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
)

func (s *errScene) jobs() *jobsPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*jobsPlugin); ok {
			return x
		}
	}
	return nil
}

// until draws frames, running what the loop was handed, until ok.
func (s *errScene) until(t *testing.T, what string, ok func() bool) {
	t.Helper()
	if !s.Until(15*time.Second, ok) {
		t.Fatalf("waiting for %s:\n%s", what, s.frameText())
	}
}

// within is the first node named label under the first node named in: a run row's button, say.
func (s *errScene) within(in, label string) *render.AccessNode {
	s.t.Helper()
	var find func(n *render.AccessNode, name string, prefix bool) *render.AccessNode
	find = func(n *render.AccessNode, name string, prefix bool) *render.AccessNode {
		if n.Name == name || prefix && strings.HasPrefix(n.Name, name) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, name, prefix); f != nil {
				return f
			}
		}
		return nil
	}
	parent := find(s.ui.AccessTree(), in, true)
	if parent == nil {
		s.t.Fatalf("nothing named %q", in)
	}
	n := find(parent, label, true)
	if n == nil {
		s.t.Fatalf("nothing named %q in %q", label, in)
	}
	return n
}

// jobRun is a job's newest run.
func jobRun(st core.State, job string) (core.SessionState, bool) {
	var out core.SessionState
	found := false
	for _, se := range st.Sessions {
		if se.Job == job && (!found || se.Created.After(out.Created) || se.Created.Equal(out.Created) && se.ID > out.ID) {
			out, found = se, true
		}
	}
	return out, found
}

// SE-4 done when: "A job saved from the page … runs as a job session against a fake provider; its row shows status,
// repos, summary … and duration; Open, Resume and Delete work; a blocked run shows in Requests."
func TestJobsRunAsJobSessions(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	x := s.jobs()
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.keyCommand(t, "<D-J>")
	if !x.open {
		t.Fatal("⌘⇧J didn't open the Jobs page")
	}
	s.until(t, "the jobs", func() bool { return !x.busy() })
	s.wantFrame(t, "JOBS", "No jobs yet")

	s.keyCommand(t, "n")
	if x.form == nil {
		t.Fatal("n didn't open the New job dialog")
	}
	s.until(t, "the agents", func() bool { return x.form != nil && x.form.agents.State().Phase == widget.Ready })
	s.typeInto(t, "Title", "Sweep stale deps")
	s.typeInto(t, "Prompt", "Bump every dependency in pricing that's behind.")
	s.click(centre(s.within("New job", "Save and run").Bounds))
	var se core.SessionState
	s.until(t, "the run", func() bool {
		var ok bool
		se, ok = jobRun(s.store.State(), "sweep-stale-deps")
		return ok
	})
	s.until(t, "the dialog closed", func() bool { return x.form == nil })
	if se.Kind != core.KindJob || se.Agent.Provider != "fake" {
		t.Errorf("the run is %s on %q", se.Kind, se.Agent.Provider)
	}
	if js := lw.w.Jobs(); len(js) != 1 || js[0].Title != "Sweep stale deps" || js[0].Prompt != "Bump every dependency in pricing that's behind." {
		t.Fatalf("the jobs: %+v", js)
	}
	a := lw.fake.WaitStarted(t)
	s.until(t, "the running row", func() bool { return strings.Contains(s.frameText(), "RUNNING") })

	ctx := context.Background()
	if err := lw.w.AddRepo(ctx, se.ID, core.RepoID(lw.repo)); err != nil {
		t.Fatal(err)
	}
	a.Call(t, "reply", map[string]any{"thread": string(se.Threads[0].ID), "body": "Bumped 3 deps in pricing; its tests pass.", "resolve": true})
	s.until(t, "the done row", func() bool { return strings.Contains(s.frameText(), "DONE") })
	row := s.within("Runs", "done run").Name
	for _, want := range []string{"Bumped 3 deps in pricing; its tests pass."} {
		if !strings.Contains(row, want) {
			t.Errorf("the row says %q, want %q", row, want)
		}
	}
	s.wantFrame(t, "pricing", "TOOK")

	// Delete asks once more, then throws the run away.
	s.click(centre(s.within("Runs", "Delete").Bounds))
	s.frame()
	s.click(centre(s.within("Runs", "Sure?").Bounds))
	waitState(t, s.store, "the run deleted", func(st core.State) bool { _, ok := st.Session(se.ID); return !ok })
	if _, err := os.Stat(se.Dir); err == nil {
		t.Error("the run's workspace is still there")
	}

	// ⌘↵ runs it again; this one stops to ask, and its question waits in Requests.
	s.frame()
	s.frame()
	s.keyCommand(t, "<D-CR>")
	s.until(t, "the second run", func() bool { _, ok := jobRun(s.store.State(), "sweep-stale-deps"); return ok })
	se, _ = jobRun(s.store.State(), "sweep-stale-deps")
	a = lw.fake.WaitStarted(t)
	question := "pricing's tests fail before any bump: is main broken?"
	a.Call(t, "reply", map[string]any{"thread": string(se.Threads[0].ID), "body": "**Blocked:** " + question})
	s.until(t, "the blocked row", func() bool { return strings.Contains(s.frameText(), "BLOCKED") })
	s.click(centre(s.within("Runs", "Open").Bounds))
	waitState(t, s.store, "the run in front", func(st core.State) bool { return st.Focus == se.ID })
	if x.open {
		t.Error("Open left the page over the session")
	}
	s.run("view.open", registry.Args{"id": RequestsView, "place": "dock", "edge": "left", "title": "Requests"})
	s.until(t, "the question in Requests", func() bool { return strings.Contains(s.frameText(), question) })
	blocked, _ := s.store.State().Session(se.ID)
	if g := groupOf(blocked.Threads()[0]); g != groupNeedsYou {
		t.Errorf("the blocked run's request is in %s, want Needs you", groupNames[g])
	}

	// A stopped run resumes.
	s.run("jobs.open", nil)
	s.run("jobs.run", nil)
	s.until(t, "the third run", func() bool { r, _ := jobRun(s.store.State(), "sweep-stale-deps"); return r.ID != se.ID })
	third, _ := jobRun(s.store.State(), "sweep-stale-deps")
	lw.fake.WaitStarted(t)
	s.run("session.stop", registry.Args{"id": string(third.ID)})
	s.until(t, "the stopped row", func() bool { return strings.Contains(s.frameText(), "STOPPED") })
	s.click(centre(s.within("stopped run", "Resume").Bounds))
	waitState(t, s.store, "the stopped run resumed", func(st core.State) bool {
		r, _ := st.Session(third.ID)
		return r.Snapshot().Agent.Running && st.Focus == third.ID
	})
}

// SE-4: "Save as job" in the launcher hands its prompt and agent to the New job dialog.
func TestSaveAsJobFromTheLauncher(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	x := s.jobs()
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.run("session.new", registry.Args{"prompt": "Draft this week's changelog from the PRs merged since Monday."})
	var l *launcher
	for _, e := range s.plugins {
		if v, ok := e.(*launcher); ok {
			l = v
		}
	}
	s.until(t, "the launcher's agents", func() bool { return l.provider != "" })
	s.click(centre(s.access("Save as job").Bounds))
	if l.open || x.form == nil {
		t.Fatalf("Save as job: launcher open %v, dialog %v", l.open, x.form != nil)
	}
	if x.form.prompt != "Draft this week's changelog from the PRs merged since Monday." || x.form.agent != "fake" {
		t.Errorf("the dialog holds %q on %q", x.form.prompt, x.form.agent)
	}
	s.until(t, "the dialog's agents", func() bool { return x.form.agents.State().Phase == widget.Ready })
	s.frame()
	s.typeInto(t, "Title", "Weekly changelog draft")
	s.click(centre(s.within("New job", "Save").Bounds))
	s.until(t, "the job saved", func() bool { return x.form == nil })
	js := lw.w.Jobs()
	if len(js) != 1 || js[0].Name != "weekly-changelog-draft" || js[0].Provider != "fake" || !x.open {
		t.Errorf("jobs %+v, page open %v", js, x.open)
	}
	if n := len(s.store.State().Sessions); n != 0 {
		t.Errorf("Save ran it: %d sessions", n)
	}
}

// SE-4: ":Jobs, ⌘⇧J and the sidebar entry open it", as do the start view's link; Stats kind=job opens Stats on
// the job runs.
func TestJobsEntryPoints(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	x := s.jobs()
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.frame()
	if !s.reg.Available("jobs.open", nil) || s.access("Jobs").Disabled {
		t.Error("the sidebar's Jobs is still dimmed")
	}
	s.click(centre(s.access("Jobs").Bounds))
	if !x.open {
		t.Error("the sidebar's Jobs didn't open the page")
	}
	x.open = false
	s.frame()
	s.click(centre(s.startCard("Jobs").Bounds))
	if !x.open {
		t.Error("the start view's Jobs link didn't open the page")
	}
	x.open = false
	s.run("Jobs", nil)
	if !x.open {
		t.Error(":Jobs didn't open the page")
	}
	s.frame()
	s.click(centre(s.within("Jobs page", "Stats").Bounds))
	s.frame()
	st := s.sh.stats
	if x.open || !st.open || len(st.q.filters["kind"]) != 1 || st.q.filters["kind"][0] != "job" {
		t.Errorf("Stats kind=job: jobs %v, stats %v %v", x.open, st.open, st.q.filters)
	}
	var keys []string
	for _, src := range s.reg.Pickers() {
		if src.ID == "pages" {
			for _, it := range src.Items(context.Background(), "") {
				keys = append(keys, it.Label+" "+it.Key)
			}
		}
	}
	if got := strings.Join(keys, ", "); !strings.Contains(got, "Jobs ⇧⌘J") {
		t.Errorf("pages = %s", got)
	}
}

// fakeSandboxes runs a sandboxed agent the way the fake provider runs any: on nothing.
type fakeSandboxes struct{ backends []string }

func (fakeSandboxes) Open(context.Context, core.SessionState, host.Approve) (host.Sandbox, error) {
	return fakeSandbox{}, nil
}

func (f fakeSandboxes) Backends() []string { return f.backends }

type fakeSandbox struct{}

func (fakeSandbox) Exec() agent.Exec     { return nopExec{} }
func (fakeSandbox) Spec() *agent.Sandbox { return &agent.Sandbox{} }
func (fakeSandbox) Close() error         { return nil }

// clock is the session host's clock, which a test moves.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// jobsRig is mockup 44's machine, mbp: api, web and pricing, six jobs, and their runs with transcripts to cost them.
type jobsRig struct {
	t      *testing.T
	w      *host.Host
	claude *loggedFake
	clock  *clock
	repos  map[string]string
}

// newJobsRig is the rig, its session host offering sandboxes in those backends.
func newJobsRig(t *testing.T, sandboxes ...string) *jobsRig {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	t.Setenv("USER", "tester")
	r := &jobsRig{t: t, claude: &loggedFake{Fake: agenttest.NewFake(), logs: map[string]string{}}, clock: &clock{t: statsNow},
		repos: map[string]string{}}
	r.claude.Name = "claude"
	root := t.TempDir()
	for _, name := range []string{"api", "web", "pricing"} {
		g := &gittest.Repo{T: t, Dir: filepath.Join(root, name), When: time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)}
		os.MkdirAll(g.Dir, 0o755)
		g.Git("init", "-q", "-b", "main")
		g.Write(map[string]string{"README": name + "\n"})
		g.Commit("Ada", "first")
		r.repos[name] = g.Dir
	}
	providers := &agent.Providers{}
	providers.Register(r.claude)
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: providers, Exec: nopExec{}, Load: steadyLoad, Now: r.clock.now,
		Sandbox: fakeSandboxes{sandboxes}, Packs: func(name string) (agent.Pack, bool) { return agent.Pack{Name: name, Skill: name}, true }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	r.w = w
	ctx := context.Background()
	for _, name := range []string{"api", "web", "pricing"} {
		if _, err := w.AddRepoPath(ctx, r.repos[name]); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func (r *jobsRig) connect(*testing.T) func(*Config) {
	return func(cfg *Config) { cfg.Host, cfg.Store = r.w, r.w.Store() }
}

func (r *jobsRig) save(at time.Time, j host.Job) host.Job {
	r.t.Helper()
	r.clock.set(at)
	saved, err := r.w.SaveJob(j)
	if err != nil {
		r.t.Fatal(err)
	}
	return saved
}

// fxRun is a run as it went: when, for how long, in which repos (and how much of its work each), and how it ended.
type fxRun struct {
	start   time.Time
	took    time.Duration
	repos   []string
	weights []int
	status  string
	summary string
}

func (r *jobsRig) run(job string, fx fxRun) core.SessionID {
	t := r.t
	t.Helper()
	ctx := context.Background()
	r.clock.set(fx.start)
	id, err := r.w.RunJob(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	a := r.claude.WaitStarted(t)
	for _, name := range fx.repos {
		if err := r.w.AddRepo(ctx, id, core.RepoID(r.repos[name])); err != nil {
			t.Fatal(err)
		}
	}
	se, _ := r.w.Store().State().Session(id)
	dirs := map[string]string{}
	for _, p := range se.Snapshot().Projects {
		dirs[p.Name] = p.Dir
	}
	session := fxSession{model: "claude-opus-4-1", repos: fx.repos, weights: fx.weights}
	rnd := rand.New(rand.NewSource(fx.start.Unix()))
	var lines []string
	n := int(fx.took.Minutes()) * 3
	for k := 0; k < n; k++ {
		at := fx.start.Add(fx.took * time.Duration(k) / time.Duration(n))
		if k%8 == 0 {
			lines = append(lines, prompt(at, false))
		}
		lines = append(lines, request(rnd, session, dirs, at, int64(30_000+k*900), k, false)...)
	}
	if len(lines) > 0 {
		p := filepath.Join(t.TempDir(), string(id)+".jsonl")
		os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
		r.claude.mu.Lock()
		r.claude.logs[se.Snapshot().Dir] = p
		r.claude.mu.Unlock()
	}
	r.clock.set(fx.start.Add(fx.took))
	tid := string(se.Snapshot().Threads[0].ID)
	switch fx.status {
	case host.RunDone:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.summary, "resolve": true})
		r.stop(id)
	case host.RunBlocked:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": "**Blocked:** " + fx.summary})
		r.stop(id)
	case host.RunRunning:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.summary, "working": true})
	case host.RunStopped:
		a.Call(t, "reply", map[string]any{"thread": tid, "body": fx.summary, "working": true})
		r.stop(id)
	}
	return id
}

// stop stops a run's agent, as a finished run's is once Oriel starts again.
func (r *jobsRig) stop(id core.SessionID) {
	r.t.Helper()
	if err := r.w.Stop(context.Background(), id); err != nil {
		r.t.Fatal(err)
	}
}

func day44(month time.Month, d, h, m int) time.Time {
	return time.Date(2026, month, d, h, m, 0, 0, time.UTC)
}

// build is 44's six jobs and their runs.
func (r *jobsRig) build() {
	sweep := "In api, web and pricing, bump every dependency that's behind on a minor or patch version. Keep major versions where they are.\n" +
		"Run each repo's tests after bumping. If one bump breaks them, back that bump out and say why in the summary; don't fix code to fit it.\n" +
		"Open one PR per repo titled `chore(deps): sweep`, linked to each other. Stop as blocked if a repo won't build before you start."
	opus := host.Job{Provider: "claude", Model: "claude-opus-4-1", Effort: "high"}
	job := func(at time.Time, title, prompt, pin string, s host.Schedule) string {
		j := opus
		j.Title, j.Prompt, j.Sandbox, j.Schedule = title, prompt, pin, s
		return r.save(at, j).Name
	}
	changelog := job(day44(10, 12, 9, 0), "Weekly changelog draft", "Draft this week's changelog for api and web from the PRs merged since last Monday.", "",
		host.Schedule{})
	_ = changelog
	pts := job(day44(10, 1, 9, 0), "Pricing test sweep", "In pricing, find the slowest tests and say why they're slow.", "sandbox", host.Schedule{})
	feed := job(day44(9, 20, 9, 0), "Price-feed backfill", "Backfill the price feed in pricing for the days the importer missed.", "host", host.Schedule{})
	flaky := job(day44(9, 12, 9, 0), "Triage flaky e2e on main", "Find which web e2e tests flaked on main this week, and why.", "sandbox", host.Schedule{})
	ci := job(day44(9, 5, 9, 0), "Morning CI triage on my open PRs", "For each of my open PRs in api and web, say what CI failed and why.", "sandbox",
		host.Schedule{Every: host.EveryWeekday, At: "07:00"})
	deps := job(day44(8, 20, 9, 0), "Sweep stale deps, keep major versions", sweep, "sandbox", host.Schedule{})
	all := []string{"api", "web", "pricing"}
	for _, fx := range []fxRun{
		{day44(8, 26, 7, 0), 24 * time.Minute, all, []int{3, 2, 1}, host.RunDone, "3 PRs · 22 bumps"},
		{day44(9, 2, 7, 0), 21 * time.Minute, all, []int{3, 2, 2}, host.RunDone, "3 PRs · 30 bumps"},
		{day44(9, 9, 7, 0), 19 * time.Minute, all, []int{4, 2, 2}, host.RunDone, "3 PRs · 27 bumps · one bump backed out (grpc 1.66)"},
		{day44(9, 16, 9, 12), 3 * time.Minute, []string{"api"}, []int{1}, host.RunStopped, "Listing what's behind in api."},
		{day44(9, 23, 7, 0), 18 * time.Minute, []string{"api", "pricing"}, []int{3, 2}, host.RunDone, "2 PRs · 18 bumps · web already current"},
		{day44(9, 30, 7, 0), 9 * time.Minute, []string{"web"}, []int{1}, host.RunBlocked,
			"react-router 6.28 breaks loaders in 3 routes; held at 6.27. Needs a call: bump and fix, or pin?"},
		{day44(10, 7, 7, 0), 22 * time.Minute, all, []int{5, 4, 3}, host.RunDone, "3 PRs: api#812, web#4431, pricing#212 · 31 bumps · all green"},
		{day44(10, 14, 12, 13), 4 * time.Minute, []string{"api", "web"}, []int{5, 1}, host.RunRunning,
			"Bumping 14 deps in api; go test running. web next, pricing not added yet."},
	} {
		r.run(deps, fx)
	}
	r.run(ci, fxRun{day44(10, 14, 7, 0), 14 * time.Minute, []string{"api", "web"}, []int{1, 1}, host.RunDone, "No failing CI on your 4 open PRs."})
	r.run(flaky, fxRun{day44(10, 13, 16, 40), 11 * time.Minute, []string{"web"}, []int{1}, host.RunBlocked,
		"Two flaky specs share a fixture; fix the fixture or quarantine them?"})
	r.run(feed, fxRun{day44(10, 13, 11, 2), 16 * time.Minute, []string{"pricing"}, []int{1}, host.RunDone, "Backfilled Oct 3–9: 7 days, 12,400 rows."})
	r.run(pts, fxRun{day44(10, 11, 9, 0), 6 * time.Minute, []string{"pricing"}, []int{1}, host.RunStopped, "Timing the suite."})
	r.clock.set(statsNow)
}

// jobsScene is the app on 44's machine and clock, the Jobs page open.
func jobsScene(t *testing.T) (*errScene, *jobsRig, *jobsPlugin) {
	t.Helper()
	r := newJobsRig(t)
	r.build()
	s := newSceneWith(t, "", "", mockupSize, r.connect(t))
	s.now = statsNow
	x := s.jobs()
	x.now, x.loc = func() time.Time { return s.now }, time.UTC
	s.sh.stats.now, s.sh.stats.loc = x.now, time.UTC
	st := waitState(t, s.store, "the runs", func(st core.State) bool { return !st.Machine.Sampled.IsZero() && len(st.Sessions) == 12 })
	for _, se := range st.Sessions {
		if host.RunOf(se).Status == host.RunRunning {
			s.store.Apply(core.SessionFocused{ID: se.ID})
		}
	}
	return s, r, x
}

func (s *errScene) settleJobs(t *testing.T, x *jobsPlugin) {
	t.Helper()
	s.until(t, "the jobs", func() bool { return !x.busy() })
	s.settleStats(s.sh.stats)
}

// SE-4 done when: "The page matches 44's panels from fixture jobs and runs": the list, the selected job's prompt,
// schedule and runs, a run's cost split by repo under the pointer (panel 1), and the New job dialog (panel 2).
func TestJobsPageSnapshots(t *testing.T) {
	s, _, x := jobsScene(t)
	s.keyCommand(t, "<D-J>")
	s.settleJobs(t, x)
	s.settleJobs(t, x)
	row := s.within("Runs", "done run, Oct 7")
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(s.within(row.Name, "Cost").Bounds)})
	s.frame()
	s.frame()
	s.lint("the jobs page")
	s.golden("jobs-page")
	s.wantFrame(t, "JOBS", "6 jobs · 1 running · 12 runs", "Sweep stale deps, keep major versions", "Morning CI triage on my open PRs",
		"never run", "PROMPT", "SCHEDULE", "EVERY WEEKDAY 07:00", "RUNS", "react-router 6.28 breaks loaders", "Stopped by you after 3m", "2 more",
		"since Aug 26", "Oct 7 run", "session stats ›", "22m", "4m")
	if r := s.within("Runs", "running run"); !strings.Contains(r.Name, "today 12:13") {
		t.Errorf("the running row: %q", r.Name)
	}
	// Each run's cost is split by the repos it worked in: the split adds up to it.
	rows := x.rows()
	for _, rr := range rows[0].runs {
		cost, shares, ok := x.cost(rr)
		sum := 0.0
		for _, sh := range shares {
			sum += sh.Cost
		}
		if !ok || len(shares) != len(rr.run.Repos) || fmt.Sprintf("%.2f", sum) != fmt.Sprintf("%.2f", cost) {
			t.Errorf("%s: cost %v, shares %+v", rr.se.ID, cost, shares)
		}
	}

	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 5, Y: 990}})
	s.keyCommand(t, "n")
	s.until(t, "the agents", func() bool { return x.form != nil && x.form.agents.State().Phase == widget.Ready })
	s.typeInto(t, "Title", "Weekly changelog draft")
	s.typeInto(t, "Prompt", "Draft this week's changelog for api and web from the PRs merged since last Monday.")
	s.lint("the new job dialog")
	s.golden("jobs-new")
	s.wantFrame(t, "New job", "TITLE", "PROMPT", "RUNS IN", "AGENT", "SCHEDULE", "Save and run", "weekly-changelog-draft.json")

}

// SE-5 done when: "⌘⇧J over Stats leaves only Jobs; the same for each pair of pages."
func TestPagesReplaceEachOther(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	x, st, set := s.jobs(), s.sh.stats, s.settings()
	pages := []struct {
		name string
		open func()
		up   func() bool
	}{
		{"Jobs", func() { s.keyCommand(t, "<D-J>") }, func() bool { return x.open }},
		{"Stats", func() { s.run("stats.open", nil) }, func() bool { return st.open }},
		{"Usage", func() { s.run("usage.open", nil) }, func() bool { return st.usageOpen }},
		{"Settings", func() { s.run("settings.open", nil) }, func() bool { return set.open }},
	}
	for _, a := range pages {
		for _, b := range pages {
			if a.name == b.name {
				continue
			}
			a.open()
			s.frame()
			b.open()
			s.frame()
			s.frame()
			for _, p := range pages {
				if p.up() != (p.name == b.name) {
					t.Errorf("%s over %s: %s is up %v", b.name, a.name, p.name, p.up())
				}
			}
		}
	}
}

// SE-5 done when: "Open on a run shows panel 3's layout; a stopped run says who stopped it; a click on a row selects
// it." Panel 3: the run's session tab ("job: <title>"), its console as the main tab, the narrow sidebar, the job beside
// the console.
func TestARunOpensInItsSessionTab(t *testing.T) {
	s, _, x := jobsScene(t)
	s.keyCommand(t, "<D-J>")
	s.settleJobs(t, x)
	s.wantFrame(t, "Stopped by you after 3m")

	focus := s.store.State().Focus
	done := s.within("Runs", "done run, Oct 7")
	s.click(render.Point{X: done.Bounds.Min.X + 300, Y: centre(done.Bounds).Y})
	s.frame()
	if !x.open || s.store.State().Focus != focus {
		t.Fatalf("a click on a row opened it: page open %v, focus %s", x.open, s.store.State().Focus)
	}
	if sel := s.within("Runs", "done run, Oct 7"); !sel.Selected {
		t.Errorf("a click didn't select the row: %+v", sel)
	}

	running := s.within("Runs", "running run")
	s.click(centre(s.within(running.Name, "Open").Bounds))
	s.until(t, "the window asked to open on the run", func() bool { return len(s.sh.reopen) > 0 })
	s.reopen()
	x = s.jobs()
	x.now, x.loc = func() time.Time { return s.now }, time.UTC
	s.sh.stats.now, s.sh.stats.loc = x.now, time.UTC
	s.until(t, "the run in front, its console the main tab", func() bool {
		for _, v := range s.sh.layout.root.Visible() {
			if w := s.sh.layout.root.Find(v); v.ID == AgentConsoleView && w.Placement == layout.Tab {
				return true
			}
		}
		return false
	})
	var r *rail
	for _, e := range s.plugins {
		if v, ok := e.(*rail); ok {
			r = v
		}
	}
	if f := r.formNow(projectsHere(s.host)); f != "narrow" {
		t.Errorf("the sidebar is %s, want narrow", f)
	}
	s.settleJobs(t, x)
	s.lint("a run's console")
	s.golden("jobs-run")
	s.wantFrame(t, "job: sweep stale deps", "JOB", "Sweep stale deps, keep major", "Jobs ›", "RUNNING", "12:13 · 4m", "done · 22m",
		"REPOS TOUCHED", "not yet", "total est.", "WHEN IT ENDS")
}

// SE-5 done when: "A run skipped while one is going shows in the list".
func TestASkippedRunShowsInTheList(t *testing.T) {
	r := newJobsRig(t)
	j := r.save(day44(10, 13, 6, 0), host.Job{Title: "Morning CI triage", Prompt: "Say what CI failed on my open PRs in api.",
		Schedule: host.Schedule{Every: host.EveryWeekday, At: "07:00"}})
	r.clock.set(day44(10, 13, 7, 0).Add(30 * time.Second))
	r.w.RunDue(context.Background())
	r.claude.WaitStarted(t)
	r.clock.set(day44(10, 14, 7, 0).Add(30 * time.Second))
	r.w.RunDue(context.Background())
	r.clock.set(statsNow)
	s := newSceneWith(t, "", "", mockupSize, r.connect(t))
	s.now = statsNow
	x := s.jobs()
	x.now, x.loc = func() time.Time { return s.now }, time.UTC
	waitState(t, s.store, "the run", func(st core.State) bool { _, ok := jobRun(st, j.Name); return ok })
	s.run("jobs.open", nil)
	s.settleJobs(t, x)
	s.run("jobs.open", registry.Args{"job": j.Name})
	s.wantFrame(t, "SKIPPED", "skipped (last run still going)", "RUNNING")
	if row := s.within("Runs", "skipped run"); !strings.Contains(row.Name, "today 07:00") {
		t.Errorf("the skipped row: %q", row.Name)
	}
}

// SE-5 done when: "The job form's and launcher's prompts take several lines; ⌘⏎ submits."
func TestTheJobPromptTakesLines(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	x := s.jobs()
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.run("jobs.new", nil)
	s.until(t, "the agents", func() bool { return x.form != nil && x.form.agents.State().Phase == widget.Ready })
	s.typeInto(t, "Prompt", "Sweep stale deps.")
	s.typeKeys("CR")
	for _, r := range "Keep majors." {
		s.typeKeys(string(r))
	}
	if x.form == nil || x.form.prompt != "Sweep stale deps.\nKeep majors." {
		t.Fatalf("⏎ in the prompt: %+v", x.form)
	}
	s.keyCommand(t, "<D-CR>")
	s.until(t, "the run", func() bool { _, ok := jobRun(s.store.State(), "sweep-stale-deps"); return ok })
	if js := lw.w.Jobs(); len(js) != 1 || js[0].Prompt != "Sweep stale deps.\nKeep majors." {
		t.Errorf("the job: %+v", js)
	}
}

// SE-5: the launcher's prompt takes several lines too; ⌘⏎ starts.
func TestTheLauncherPromptTakesLines(t *testing.T) {
	lw := newLocalHost(t)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := s.host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil { // no sandbox here
		t.Fatal(err)
	}
	s.run("session.new", registry.Args{"kind": "scratch"})
	var l *launcher
	for _, e := range s.plugins {
		if v, ok := e.(*launcher); ok {
			l = v
		}
	}
	s.until(t, "the launcher's agents", func() bool { return l.provider != "" })
	s.typeInto(t, "Prompt", "notes")
	s.typeKeys("CR", "x")
	if l.prompt != "notes\nx" || !l.open {
		t.Fatalf("⏎ in the launcher's prompt: %q, open %v", l.prompt, l.open)
	}
	s.keyCommand(t, "<D-CR>")
	if a := lw.fake.WaitStarted(t); a == nil {
		t.Fatal("⌘⏎ didn't start it")
	}
	waitState(t, s.store, "the session", func(st core.State) bool {
		return len(st.Sessions) == 1 && st.Sessions[0].Prompt == "notes\nx"
	})
}
