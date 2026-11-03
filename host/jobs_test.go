package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
)

// One JSON file per job under ~/.oriel/jobs on the worker, named by a slug of the title, which comes from the
// prompt's first line when there's none (crit's internal/job).
func TestJobsAreOneFileEach(t *testing.T) {
	r := newRig(t)
	j, err := r.w.SaveJob(Job{Prompt: "# Sweep stale deps, keep major versions\nIn api, web and pricing, bump every dependency."})
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "sweep-stale-deps-keep-major-versions" || j.Title != "Sweep stale deps, keep major versions" {
		t.Errorf("job = %q %q", j.Name, j.Title)
	}
	b, err := os.ReadFile(filepath.Join(r.home, "jobs", j.Name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk Job
	if err := json.Unmarshal(b, &onDisk); err != nil || onDisk.Prompt != j.Prompt || !onDisk.CreatedAt.Equal(r.now()) {
		t.Errorf("on disk: %s (%v)", b, err)
	}
	// A new job whose name is taken steps aside; an edit names its job and rewrites it.
	again, _ := r.w.SaveJob(Job{Title: j.Title, Prompt: "something else"})
	if again.Name != j.Name+"-2" {
		t.Errorf("second job = %q", again.Name)
	}
	r.setNow(r.now().Add(time.Hour))
	j.Prompt, j.Schedule = "Bump the deps.", Schedule{Every: EveryWeekday, At: "07:00"}
	edited, err := r.w.SaveJob(j)
	if err != nil || edited.Name != j.Name || edited.Prompt != "Bump the deps." || !edited.CreatedAt.Equal(onDisk.CreatedAt) || !edited.UpdatedAt.Equal(r.now()) {
		t.Errorf("edited = %+v (%v)", edited, err)
	}
	if _, err := r.w.SaveJob(Job{Title: "nothing to do"}); err == nil {
		t.Error("a job with no prompt saved")
	}
	jobs := r.w.Jobs()
	if len(jobs) != 2 || jobs[0].Name != j.Name {
		t.Errorf("jobs = %+v", jobs)
	}
	if err := r.w.DeleteJob(again.Name); err != nil {
		t.Fatal(err)
	}
	if len(r.w.Jobs()) != 1 {
		t.Error("the deleted job is still listed")
	}
}

// A run is a session of kind job in a fresh workspace, given the prompt as JOB.md, on the job's pins.
func TestAJobRunsAsAJobSession(t *testing.T) {
	r := newRig(t)
	j, _ := r.w.SaveJob(Job{Title: "Sweep stale deps", Prompt: "Bump every dependency that's behind.", Effort: "high"})
	id, err := r.w.RunJob(context.Background(), j.Name)
	if err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	if se.Kind != core.KindJob || JobOf(se) != j.Name || len(se.Projects) != 0 || se.Agent.Provider != "fake" || se.Agent.Effort != "high" {
		t.Errorf("run = %+v", se)
	}
	b, err := os.ReadFile(filepath.Join(se.Dir, "JOB.md"))
	if err != nil || string(b) != j.Prompt+"\n" {
		t.Errorf("JOB.md = %q (%v)", b, err)
	}
	a := r.agent()
	if a.Spec.Pack.Name != "job" {
		t.Errorf("pack = %q", a.Spec.Pack.Name)
	}
	if got := RunOf(r.session(id)); got.Status != RunRunning {
		t.Errorf("status = %q, want running", got.Status)
	}
	// Run tabs read "job: <title>".
	if se.Name != "job: sweep stale deps" {
		t.Errorf("the run's name = %q", se.Name)
	}
	if _, err := r.w.RunJob(context.Background(), "nope"); err == nil {
		t.Error("ran a job that isn't there")
	}
}

// A run's row is its status, the repos it worked in, the agent's summary and how long it took. Done is the request
// resolved; blocked is the agent's answer left open (job.md: "reply starting **Blocked:**"); stopped is stopped by
// you before either.
func TestRunStatuses(t *testing.T) {
	r := newRig(t)
	j, _ := r.w.SaveJob(Job{Prompt: "Sweep stale deps"})
	run := func() (core.SessionID, string, *agenttest.FakeSession) {
		id, err := r.w.RunJob(context.Background(), j.Name)
		if err != nil {
			t.Fatal(err)
		}
		a := r.agent()
		return id, string(r.session(id).Threads[0].ID), a
	}
	ctx := context.Background()

	id, tid, a := run()
	if err := r.w.AddRepo(ctx, id, core.RepoID(r.repo.Dir)); err != nil {
		t.Fatal(err)
	}
	r.setNow(r.now().Add(22 * time.Minute))
	reply := func(a *agenttest.FakeSession, body string, resolve bool) {
		t.Helper()
		if _, err := a.CallErr("reply", map[string]any{"thread": tid, "body": body, "resolve": resolve}); err != nil {
			t.Fatal(err)
		}
	}
	reply(a, "3 PRs: api#812, web#4431.\n\nAll green.", true)
	got := RunOf(r.session(id))
	if got.Status != RunDone || got.Summary != "3 PRs: api#812, web#4431. All green." || got.Took() != 22*time.Minute ||
		len(got.Repos) != 1 || got.Repos[0] != filepath.Base(r.repo.Dir) {
		t.Errorf("done run = %+v", got)
	}

	id, tid, a = run()
	reply(a, "**Blocked:** react-router 6.28 breaks loaders. Needs a call: bump and fix, or pin?", false)
	if got := RunOf(r.session(id)); got.Status != RunBlocked || got.Summary != "react-router 6.28 breaks loaders. Needs a call: bump and fix, or pin?" {
		t.Errorf("blocked run = %+v", got)
	}
	if se := r.session(id); se.Status != core.StatusNeedsYou {
		t.Errorf("a blocked run's session is %s, want needs_you: it's in Requests", se.Status)
	}

	id, _, _ = run()
	if err := r.w.Stop(ctx, id); err != nil {
		t.Fatal(err)
	}
	if got := RunOf(r.eventually(id, "stopped", func(se core.SessionState) bool { return !se.Agent.Running })); got.Status != RunStopped ||
		got.StoppedBy != core.StoppedByYou {
		t.Errorf("stopped run = %+v", got)
	}
}

// A stop records who (you, the agent, the worker on shutdown), so the row says "Stopped by you".
func TestAStopSaysWho(t *testing.T) {
	r := newRig(t)
	j, _ := r.w.SaveJob(Job{Prompt: "Sweep stale deps"})
	ctx := context.Background()
	quits, _ := r.w.RunJob(ctx, j.Name)
	r.agent().Close() // the agent's process ends by itself
	if got := RunOf(r.eventually(quits, "stopped", func(se core.SessionState) bool { return !se.Agent.Live() })); got.StoppedBy != core.StoppedByAgent {
		t.Errorf("an agent that quit: %+v", got)
	}
	shut, _ := r.w.RunJob(ctx, j.Name)
	r.agent()
	r.restart()
	if got := RunOf(r.session(shut)); got.Status != RunStopped {
		t.Errorf("a run stopped as the host closed: %+v", got)
	}
	if err := r.w.Resume(ctx, shut); err != nil {
		t.Fatal(err)
	}
	r.agent()
	if se := r.eventually(shut, "running", func(se core.SessionState) bool { return se.Agent.Running }); se.Agent.StoppedBy != "" {
		t.Errorf("resumed, it still says stopped by %q", se.Agent.StoppedBy)
	}
}

// Occurrences missed while the worker was off are run once when it starts (one per job, not a backlog).
func TestAMissedOccurrenceRunsOnceAfterARestart(t *testing.T) {
	r := newRig(t)
	r.setNow(time.Date(2026, 10, 12, 6, 0, 0, 0, time.UTC)) // Monday
	r.restart()
	j, err := r.w.SaveJob(Job{Prompt: "Triage CI on my open PRs", Schedule: Schedule{Every: EveryWeekday, At: "07:00"}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := r.w.SaveJob(Job{Prompt: "Weekly changelog", Schedule: Schedule{Every: EveryWeek, Days: []time.Weekday{time.Sunday}, At: "09:00"}})
	runs := func(name string) int {
		n := 0
		for _, se := range r.w.store.State().Sessions {
			if JobOf(se) == name {
				n++
			}
		}
		return n
	}
	r.w.Close()
	r.setNow(time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)) // Wednesday: Monday's, Tuesday's and Wednesday's 07:00 missed
	r.open()
	r.w.RunDue(context.Background())
	if n := runs(j.Name); n != 1 {
		t.Fatalf("after three missed occurrences, %d runs; want one", n)
	}
	if n := runs(other.Name); n != 0 {
		t.Errorf("a job with nothing missed ran %d times", n)
	}
	r.setNow(r.now().Add(time.Minute))
	r.w.RunDue(context.Background())
	if n := runs(j.Name); n != 1 {
		t.Errorf("the next look ran it again: %d runs", n)
	}
}

// A skipped run is kept in the run list as "skipped (last run still going)": the job keeps each one.
func TestSkippedRunsAreKept(t *testing.T) {
	r := newRig(t)
	r.setNow(time.Date(2026, 10, 14, 6, 59, 0, 0, time.UTC))
	r.restart()
	j, _ := r.w.SaveJob(Job{Prompt: "Triage CI", Schedule: Schedule{Every: EveryWeekday, At: "07:00"}})
	r.setNow(time.Date(2026, 10, 14, 7, 0, 30, 0, time.UTC))
	r.w.RunDue(context.Background())
	r.agent()
	for _, day := range []int{15, 16} {
		r.setNow(time.Date(2026, 10, day, 7, 0, 30, 0, time.UTC))
		r.w.RunDue(context.Background())
	}
	got := r.w.Jobs()[0].Skips
	if len(got) != 2 || got[0].Day() != 15 || got[1].Day() != 16 {
		t.Errorf("skips = %v", got)
	}
	j.Prompt = "Triage CI, quickly"
	if saved, _ := r.w.SaveJob(j); len(saved.Skips) != 2 {
		t.Errorf("an edit forgot the skips: %v", saved.Skips)
	}
}

// Delete throws the workspace away: its worktrees and the session go, the repos stay.
func TestDeleteThrowsARunAway(t *testing.T) {
	r := newRig(t)
	j, _ := r.w.SaveJob(Job{Prompt: "Sweep stale deps"})
	ctx := context.Background()
	id, _ := r.w.RunJob(ctx, j.Name)
	r.agent()
	if err := r.w.AddRepo(ctx, id, core.RepoID(r.repo.Dir)); err != nil {
		t.Fatal(err)
	}
	se := r.session(id)
	os.WriteFile(filepath.Join(se.Projects[0].Dir, "wip.txt"), []byte("uncommitted"), 0o644)
	if err := r.w.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := r.w.session(id); err == nil {
		t.Error("the session is still there")
	}
	for _, dir := range []string{se.Dir, r.w.sessionDir(id)} {
		if _, err := os.Stat(dir); err == nil {
			t.Errorf("%s is still there", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(r.repo.Dir, "round.go")); err != nil {
		t.Error("the repo itself went")
	}
	if out := r.repo.Git("worktree", "list"); strings.Contains(out, se.Dir) {
		t.Errorf("the worktree is still registered:\n%s", out)
	}
	r.restart()
	if _, err := r.w.session(id); err == nil {
		t.Error("the deleted run came back after a restart")
	}
	if len(r.w.Jobs()) != 1 {
		t.Error("deleting a run deleted its job")
	}
}

// A schedule fires a run at its time on a fake clock and skips while one is running.
func TestScheduleFiresAndSkipsWhileRunning(t *testing.T) {
	r := newRig(t)
	r.setNow(time.Date(2026, 10, 14, 6, 59, 0, 0, time.UTC))
	r.restart()
	j, err := r.w.SaveJob(Job{Prompt: "Triage CI on my open PRs", Schedule: Schedule{Every: EveryWeekday, At: "07:00"}})
	if err != nil {
		t.Fatal(err)
	}
	runs := func() []core.SessionState {
		var out []core.SessionState
		for _, se := range r.w.store.State().Sessions {
			if JobOf(se) == j.Name {
				out = append(out, se)
			}
		}
		return out
	}
	r.w.RunDue(context.Background())
	if n := len(runs()); n != 0 {
		t.Fatalf("%d runs before 07:00", n)
	}
	r.setNow(time.Date(2026, 10, 14, 7, 0, 20, 0, time.UTC))
	r.w.RunDue(context.Background())
	if n := len(runs()); n != 1 {
		t.Fatalf("%d runs at 07:00, want 1", n)
	}
	a := r.agent()
	r.w.RunDue(context.Background())
	if n := len(runs()); n != 1 {
		t.Fatalf("a second run in the same minute: %d", n)
	}
	// Thursday 07:00, and Wednesday's run is still going: skipped.
	r.setNow(time.Date(2026, 10, 15, 7, 0, 5, 0, time.UTC))
	r.w.RunDue(context.Background())
	if n := len(runs()); n != 1 {
		t.Fatalf("a run started while the last was still going: %d", n)
	}
	if got := r.w.Jobs()[0].Skips; len(got) != 1 || !got[0].Equal(time.Date(2026, 10, 15, 7, 0, 0, 0, time.UTC)) {
		t.Errorf("skips = %v", got)
	}
	tid := string(runs()[0].Threads[0].ID)
	a.Call(t, "reply", map[string]any{"thread": tid, "body": "Nothing failing.", "resolve": true})
	r.setNow(time.Date(2026, 10, 16, 7, 1, 0, 0, time.UTC))
	r.w.RunDue(context.Background())
	if n := len(runs()); n != 2 {
		t.Fatalf("Friday's run didn't start: %d runs", n)
	}
}
