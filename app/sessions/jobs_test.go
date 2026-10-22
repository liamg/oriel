package sessions_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/sessions"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/ui/widget"
)

// jobRun is a job's newest run.
func jobRun(st core.State, job string) (core.SessionState, bool) {
	var out core.SessionState
	found := false
	for _, se := range st.Sessions {
		if host.JobOf(se) == job && (!found || se.Created.After(out.Created) || se.Created.Equal(out.Created) && se.ID > out.ID) {
			out, found = se, true
		}
	}
	return out, found
}

// A job saved from the page … runs as a job session against a fake provider; its row shows status,
// repos, summary … and duration; Open, Resume and Delete work; a blocked run shows in Requests.
func TestJobsRunAsJobSessions(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	x := jobsOf(s)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Press("<D-J>")
	if !x.Open() {
		t.Fatal("⌘⇧J didn't open the Jobs page")
	}
	s.Until("the jobs", func() bool { return !x.Busy() })
	s.WantFrame("JOBS", "No jobs yet")

	s.Press("n")
	if _, _, open := x.Form(); !open {
		t.Fatal("n didn't open the New job dialog")
	}
	s.Until("the agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.TypeInto("Title", "Sweep stale deps")
	s.TypeInto("Prompt", "Bump every dependency in pricing that's behind.")
	s.Click(apptest.Centre(s.Within("New job", "Save and run").Bounds), 0)
	var se core.SessionState
	s.Until("the run", func() bool {
		var ok bool
		se, ok = jobRun(s.Store().State(), "sweep-stale-deps")
		return ok
	})
	s.Until("the dialog closed", func() bool { _, _, open := x.Form(); return !open })
	if se.Kind != core.KindJob || se.Agent.Provider != "fake" {
		t.Errorf("the run is %s on %q", se.Kind, se.Agent.Provider)
	}
	if js := lw.W.Jobs(); len(js) != 1 || js[0].Title != "Sweep stale deps" || js[0].Prompt != "Bump every dependency in pricing that's behind." {
		t.Fatalf("the jobs: %+v", js)
	}
	a := lw.Fake.WaitStarted(t)
	s.Until("the running row", func() bool { return strings.Contains(s.Text(), "RUNNING") })

	ctx := context.Background()
	if err := lw.W.AddRepo(ctx, se.ID, core.RepoID(lw.Repo)); err != nil {
		t.Fatal(err)
	}
	a.Call(t, "reply", map[string]any{"thread": string(se.Threads[0].ID), "body": "Bumped 3 deps in pricing; its tests pass.", "resolve": true})
	s.Until("the done row", func() bool { return strings.Contains(s.Text(), "DONE") })
	row := s.Within("Runs", "done run").Name
	for _, want := range []string{"Bumped 3 deps in pricing; its tests pass."} {
		if !strings.Contains(row, want) {
			t.Errorf("the row says %q, want %q", row, want)
		}
	}
	s.WantFrame("pricing", "TOOK")

	// Delete asks once more, then throws the run away.
	s.Click(apptest.Centre(s.Within("Runs", "Delete").Bounds), 0)
	s.Frame()
	s.Click(apptest.Centre(s.Within("Runs", "Sure?").Bounds), 0)
	rig.WaitState(t, s.Store(), "the run deleted", func(st core.State) bool { _, ok := st.Session(se.ID); return !ok })
	if _, err := os.Stat(se.Dir); err == nil {
		t.Error("the run's workspace is still there")
	}

	// ⌘↵ runs it again; this one stops to ask, and its question waits in Requests.
	s.Frame()
	s.Frame()
	s.Press("<D-CR>")
	s.Until("the second run", func() bool { _, ok := jobRun(s.Store().State(), "sweep-stale-deps"); return ok })
	se, _ = jobRun(s.Store().State(), "sweep-stale-deps")
	a = lw.Fake.WaitStarted(t)
	question := "pricing's tests fail before any bump: is main broken?"
	a.Call(t, "reply", map[string]any{"thread": string(se.Threads[0].ID), "body": "**Blocked:** " + question})
	s.Until("the blocked row", func() bool { return strings.Contains(s.Text(), "BLOCKED") })
	s.Click(apptest.Centre(s.Within("Runs", "Open").Bounds), 0)
	rig.WaitState(t, s.Store(), "the run in front", func(st core.State) bool { return st.Focus == se.ID })
	if x.Open() {
		t.Error("Open left the page over the session")
	}
	s.Run("view.open", registry.Args{"id": kit.RequestsView, "place": "dock", "edge": "left", "title": "Requests"})
	s.Until("the question in Requests", func() bool { return strings.Contains(s.Text(), question) })
	blocked, _ := s.Store().State().Session(se.ID)
	if th := blocked.Threads()[0]; !th.Open() || th.Busy || !th.AgentAnswered() || th.Btw {
		t.Errorf("the blocked run's request doesn't wait on you: %+v", th)
	}

	// A stopped run resumes.
	s.Run("jobs.open", nil)
	s.Run("jobs.run", nil)
	s.Until("the third run", func() bool { r, _ := jobRun(s.Store().State(), "sweep-stale-deps"); return r.ID != se.ID })
	third, _ := jobRun(s.Store().State(), "sweep-stale-deps")
	lw.Fake.WaitStarted(t)
	s.Run("session.stop", registry.Args{"id": string(third.ID)})
	s.Until("the stopped row", func() bool { return strings.Contains(s.Text(), "STOPPED") })
	s.Click(apptest.Centre(s.Within("stopped run", "Resume").Bounds), 0)
	s.UntilState("the stopped run resumed", func(st core.State) bool {
		r, _ := st.Session(third.ID)
		return r.Snapshot().Agent.Running && st.Focus == third.ID
	})
}

// "Save as job" in the launcher hands its prompt and agent to the New job dialog.
func TestSaveAsJobFromTheLauncher(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	x := jobsOf(s)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("session.new", registry.Args{"prompt": "Draft this week's changelog from the PRs merged since Monday."})
	l := launcherOf(s)
	s.Until("the launcher's agents", func() bool { return l.Provider() != "" })
	s.Click(apptest.Centre(s.Node("Save as job").Bounds), 0)
	prompt, agent, form := x.Form()
	if l.Open() || !form {
		t.Fatalf("Save as job: launcher open %v, dialog %v", l.Open(), form)
	}
	if prompt != "Draft this week's changelog from the PRs merged since Monday." || agent != "fake" {
		t.Errorf("the dialog holds %q on %q", prompt, agent)
	}
	s.Until("the dialog's agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.Frame()
	s.TypeInto("Title", "Weekly changelog draft")
	s.Click(apptest.Centre(s.Within("New job", "Save").Bounds), 0)
	s.Until("the job saved", func() bool { _, _, open := x.Form(); return !open })
	js := lw.W.Jobs()
	if len(js) != 1 || js[0].Name != "weekly-changelog-draft" || js[0].Provider != "fake" || !x.Open() {
		t.Errorf("jobs %+v, page open %v", js, x.Open())
	}
	if n := len(s.Store().State().Sessions); n != 0 {
		t.Errorf("Save ran it: %d sessions", n)
	}
}

// :Jobs, ⌘⇧J and the sidebar entry open it, as does the start view's link; Stats kind=job opens Stats on
// the job runs.
func TestJobsEntryPoints(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	x := jobsOf(s)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Frame()
	if !s.Registry().Available("jobs.open", nil) || s.Node("Jobs").Disabled {
		t.Error("the sidebar's Jobs is still dimmed")
	}
	s.Click(apptest.Centre(s.Node("Jobs").Bounds), 0)
	if !x.Open() {
		t.Error("the sidebar's Jobs didn't open the page")
	}
	x.Close()
	s.Frame()
	s.Click(apptest.Centre(s.StartCard("Jobs").Bounds), 0)
	if !x.Open() {
		t.Error("the start view's Jobs link didn't open the page")
	}
	x.Close()
	s.Run("Jobs", nil)
	if !x.Open() {
		t.Error(":Jobs didn't open the page")
	}
	s.Frame()
	s.Click(apptest.Centre(s.Within("Jobs page", "Stats").Bounds), 0)
	s.Frame() // Stats opens over Jobs
	s.Frame() // and Jobs gives way: one page is up at a time
	if x.Open() || s.Count("Remove kind job") != 1 || s.CountPrefix("Remove kind ") != 1 {
		t.Errorf("Stats kind=job: jobs %v, stats' kind chips:\n%s", x.Open(), s.Text())
	}
	var keys []string
	for _, src := range s.Registry().Pickers() {
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

// jobsScene is the app on a fixed machine and clock, the Jobs page open.
func jobsScene(t *testing.T) (*apptest.Scene, *rig.Jobs, *sessions.Jobs) {
	t.Helper()
	r := rig.NewJobs(t)
	r.Build()
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(r.W)})
	s.Now = rig.StatsNow
	x := jobsOf(s)
	x.SetClock(func() time.Time { return s.Now }, time.UTC)
	s.StatsClock(func() time.Time { return s.Now }, time.UTC)
	st := rig.WaitState(t, s.Store(), "the runs", func(st core.State) bool { return !st.Machine.Sampled.IsZero() && len(st.Sessions) == 12 })
	for _, se := range st.Sessions {
		if host.RunOf(se).Status == host.RunRunning {
			s.Store().Apply(core.SessionFocused{ID: se.ID})
		}
	}
	return s, r, x
}

// The Jobs page from fixture jobs and runs: the list, the selected job's prompt, schedule and runs, a run's cost
// split by repo under the pointer, and the New job dialog.
func TestJobsPageSnapshots(t *testing.T) {
	s, _, x := jobsScene(t)
	s.Press("<D-J>")
	settleJobs(s, x)
	settleJobs(s, x)
	row := s.Within("Runs", "done run, Oct 7")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.Within(row.Name, "Cost").Bounds)})
	s.Frame()
	s.Frame()
	s.Lint("the jobs page")
	s.Golden("jobs-page")
	s.WantFrame("JOBS", "6 jobs · 1 running · 12 runs", "Sweep stale deps, keep major versions", "Morning CI triage on my open PRs",
		"never run", "PROMPT", "SCHEDULE", "EVERY WEEKDAY 07:00", "RUNS", "react-router 6.28 breaks loaders", "Stopped by you after 3m", "2 more",
		"since Aug 26", "Oct 7 run", "session stats ›", "22m", "4m")
	if r := s.Within("Runs", "running run"); !strings.Contains(r.Name, "today 12:13") {
		t.Errorf("the running row: %q", r.Name)
	}
	// Each run's cost is split by the repos it worked in: the split adds up to it.
	costs, shares, repos, oks := x.RunCosts()
	for i, cost := range costs {
		sum := 0.0
		for _, sh := range shares[i] {
			sum += sh.Cost
		}
		if !oks[i] || len(shares[i]) != len(repos[i]) || fmt.Sprintf("%.2f", sum) != fmt.Sprintf("%.2f", cost) {
			t.Errorf("run %d: cost %v, shares %+v", i, cost, shares[i])
		}
	}

	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 5, Y: 990}})
	s.Press("n")
	s.Until("the agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.TypeInto("Title", "Weekly changelog draft")
	s.TypeInto("Prompt", "Draft this week's changelog for api and web from the PRs merged since last Monday.")
	s.Lint("the new job dialog")
	s.Golden("jobs-new")
	s.WantFrame("New job", "TITLE", "PROMPT", "RUNS IN", "AGENT", "SCHEDULE", "Save and run", "weekly-changelog-draft.json")

}

// ⌘⇧J over Stats leaves only Jobs; the same for each pair of pages.
func TestPagesReplaceEachOther(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	x := jobsOf(s)
	pages := []struct {
		name string
		open func()
		up   func() bool
	}{
		{"Jobs", func() { s.Press("<D-J>") }, func() bool { return x.Open() }},
		{"Stats", func() { s.Run("stats.open", nil) }, func() bool { return s.Shown("stats") }},
		{"Usage", func() { s.Run("usage.open", nil) }, func() bool { return s.Shown("usage") }},
		{"Settings", func() { s.Run("settings.open", nil) }, func() bool { return s.Shown("settings") }},
	}
	for _, a := range pages {
		for _, b := range pages {
			if a.name == b.name {
				continue
			}
			a.open()
			s.Frame()
			b.open()
			s.Frame()
			s.Frame()
			for _, p := range pages {
				if p.up() != (p.name == b.name) {
					t.Errorf("%s over %s: %s is up %v", b.name, a.name, p.name, p.up())
				}
			}
		}
	}
}

// Open on a run shows the run's layout; a stopped run says who stopped it; a click on a row selects it. The
// layout: the run's session tab ("job: <title>"), its console as the main tab, the narrow sidebar, the job beside
// the console.
func TestARunOpensInItsSessionTab(t *testing.T) {
	s, _, x := jobsScene(t)
	s.Press("<D-J>")
	settleJobs(s, x)
	s.WantFrame("Stopped by you after 3m")

	focus := s.Store().State().Focus
	done := s.Within("Runs", "done run, Oct 7")
	s.Click(render.Point{X: done.Bounds.Min.X + 300, Y: apptest.Centre(done.Bounds).Y}, 0)
	s.Frame()
	if !x.Open() || s.Store().State().Focus != focus {
		t.Fatalf("a click on a row opened it: page open %v, focus %s", x.Open(), s.Store().State().Focus)
	}
	if sel := s.Within("Runs", "done run, Oct 7"); !sel.Selected {
		t.Errorf("a click didn't select the row: %+v", sel)
	}

	running := s.Within("Runs", "running run")
	s.Click(apptest.Centre(s.Within(running.Name, "Open").Bounds), 0)
	s.Until("the window asked to open on the run", func() bool { return s.ReopenAsked() })
	s.Reopen()
	x = jobsOf(s)
	x.SetClock(func() time.Time { return s.Now }, time.UTC)
	s.StatsClock(func() time.Time { return s.Now }, time.UTC)
	s.Until("the run in front, its console the main tab", func() bool {
		for _, v := range s.Host().Layout().Visible() {
			if w := s.Host().Layout().Find(v); v.ID == kit.AgentConsoleView && w.Placement == layout.Tab {
				return true
			}
		}
		return false
	})
	if f := s.SidebarForm(); f != "narrow" {
		t.Errorf("the sidebar is %s, want narrow", f)
	}
	settleJobs(s, x)
	s.Lint("a run's console")
	s.Golden("jobs-run")
	s.WantFrame("job: sweep stale deps", "JOB", "Sweep stale deps, keep major", "Jobs ›", "RUNNING", "12:13 · 4m", "done · 22m",
		"REPOS TOUCHED", "not yet", "total est.", "WHEN IT ENDS")
}

// A run skipped while one is going shows in the list.
func TestASkippedRunShowsInTheList(t *testing.T) {
	r := rig.NewJobs(t)
	j := r.Save(rig.Day44(10, 13, 6, 0), host.Job{Title: "Morning CI triage", Prompt: "Say what CI failed on my open PRs in api.",
		Schedule: host.Schedule{Every: host.EveryWeekday, At: "07:00"}})
	r.Clock.Set(rig.Day44(10, 13, 7, 0).Add(30 * time.Second))
	r.W.RunDue(context.Background())
	r.Claude.WaitStarted(t)
	r.Clock.Set(rig.Day44(10, 14, 7, 0).Add(30 * time.Second))
	r.W.RunDue(context.Background())
	r.Clock.Set(rig.StatsNow)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(r.W)})
	s.Now = rig.StatsNow
	x := jobsOf(s)
	x.SetClock(func() time.Time { return s.Now }, time.UTC)
	rig.WaitState(t, s.Store(), "the run", func(st core.State) bool { _, ok := jobRun(st, j.Name); return ok })
	s.Run("jobs.open", nil)
	settleJobs(s, x)
	s.Run("jobs.open", registry.Args{"job": j.Name})
	s.WantFrame("SKIPPED", "skipped (last run still going)", "RUNNING")
	if row := s.Within("Runs", "skipped run"); !strings.Contains(row.Name, "today 07:00") {
		t.Errorf("the skipped row: %q", row.Name)
	}
}

// The job form's and launcher's prompts take several lines; ⌘⏎ submits.
func TestTheJobPromptTakesLines(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	x := jobsOf(s)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("jobs.new", nil)
	s.Until("the agents", func() bool { return x.FormAgents().Phase == widget.Ready })
	s.TypeInto("Prompt", "Sweep stale deps.")
	s.Type("CR")
	for _, r := range "Keep majors." {
		s.Type(string(r))
	}
	if prompt, _, open := x.Form(); !open || prompt != "Sweep stale deps.\nKeep majors." {
		t.Fatalf("⏎ in the prompt: %q, open %v", prompt, open)
	}
	s.Press("<D-CR>")
	s.Until("the run", func() bool { _, ok := jobRun(s.Store().State(), "sweep-stale-deps"); return ok })
	if js := lw.W.Jobs(); len(js) != 1 || js[0].Prompt != "Sweep stale deps.\nKeep majors." {
		t.Errorf("the job: %+v", js)
	}
}

// The launcher's prompt takes several lines too; ⌘⏎ starts.
func TestTheLauncherPromptTakesLines(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := s.Host().Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil { // no sandbox here
		t.Fatal(err)
	}
	s.Run("session.new", registry.Args{"kind": "plan"})
	l := launcherOf(s)
	s.Until("the launcher's agents", func() bool { return l.Provider() != "" })
	s.TypeInto("Prompt", "notes")
	s.Type("CR", "x")
	if l.Prompt() != "notes\nx" || !l.Open() {
		t.Fatalf("⏎ in the launcher's prompt: %q, open %v", l.Prompt(), l.Open())
	}
	s.Press("<D-CR>")
	if a := lw.Fake.WaitStarted(t); a == nil {
		t.Fatal("⌘⏎ didn't start it")
	}
	rig.WaitState(t, s.Store(), "the session", func(st core.State) bool {
		return len(st.Sessions) == 1 && st.Sessions[0].Prompt == "notes\nx"
	})
}
