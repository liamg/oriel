package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
)

// Jobs (SE-4, mockup 44): saved prompts, each run a session of kind job; the Jobs page lists them with their runs and
// schedules, built on what the session host answers (Jobs, SaveJob, RunJob, Delete).
const (
	JobsKeyMode    = "jobs"
	JobFormKeyMode = "jobs.form"
)

// The run statuses' colours.
var (
	tRunRunning = theme.Declare("jobs.run.running", "status.working")
	tRunDone    = theme.Declare("jobs.run.done", "status.quiet")
	tRunBlocked = theme.Declare("jobs.run.blocked", "status.needs_you")
	tRunStopped = theme.Declare("jobs.run.stopped", "fg.faint")
	tRunSkipped = theme.Declare("jobs.run.skipped", "fg.ghost")
)

func runTone(status string) theme.TokenID {
	switch status {
	case worker.RunRunning:
		return tRunRunning
	case worker.RunDone:
		return tRunDone
	case worker.RunBlocked:
		return tRunBlocked
	case worker.RunSkipped:
		return tRunSkipped
	}
	return tRunStopped
}

// runFilters are the runs card's filters: all, then each status.
var runFilters = []string{"", worker.RunRunning, worker.RunDone, worker.RunBlocked, worker.RunStopped}

type jobsPlugin struct {
	disposer
	sh  *shell
	h   plugin.Host
	now func() time.Time
	loc *time.Location

	open      bool
	selKey    string // the selected job's name
	runFilter int    // into runFilters
	filter    string // the job list's filter
	runSel    int    // the runs list's selection
	confirm   string // what a Delete asks "Sure?" about: a job's key or a run's session
	err       string

	form *jobForm // the New job and Edit dialog, while it's open

	mu       sync.Mutex
	jobs     fetched[[]worker.Job]
	repos    []core.Repo
	inflight bool
	stale    bool
}

// jobRow is a job with its runs, newest first.
type jobRow struct {
	job  worker.Job
	runs []runRow
}

// runRow is a run, or a scheduled one that was skipped (no session).
type runRow struct {
	se  core.SessionState
	run worker.Run
}

func (r runRow) skipped() bool { return r.run.Status == worker.RunSkipped }

// start is when it started, or was due.
func (r runRow) start() time.Time {
	if r.skipped() {
		return r.run.Started
	}
	return r.se.Created
}

// skipRows are a job's skipped occurrences as rows.
func skipRows(j worker.Job) []runRow {
	var out []runRow
	for _, at := range j.Skips {
		out = append(out, runRow{run: worker.Run{Status: worker.RunSkipped, Started: at, Summary: "skipped (last run still going)"}})
	}
	return out
}

func jobKey(j worker.Job) string { return j.Name }

func (r jobRow) last() (runRow, bool) {
	if len(r.runs) == 0 {
		return runRow{}, false
	}
	return r.runs[0], true
}

func (x *jobsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "jobs"} }

func (x *jobsPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	if x.loc == nil {
		x.loc = time.Local
	}
	has := func(registry.Args) bool { return x.sh.sessions != nil && x.sh.cfg.Worker != nil }
	formOpen := func(registry.Args) bool { return x.form != nil }
	for _, c := range []registry.Command{
		{ID: "Jobs", Title: "The Jobs page; :Jobs {job}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(strings.TrimSpace(a.String("args")))
		}},
		{ID: "jobs.open", Title: "Open the Jobs page, at a {job} by name", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("job"))
		}},
		{ID: "jobs.new", Title: "New job: the dialog, with {title}, {prompt}, {sandbox}, {provider}, {model}, {effort} or {schedule} filled in",
			Available: has, Run: func(_ registry.Ctx, a registry.Args) error { x.newForm(a); return nil }},
		{ID: "jobs.edit", Title: "Edit a job in the dialog: {name}, the selected one by default", Available: has,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.editForm(a, false) }},
		{ID: "jobs.duplicate", Title: "Duplicate a job in the New job dialog: {name}, the selected one by default", Available: has,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.editForm(a, true) }},
		{ID: "jobs.save", Title: "Save a job: a new one, or {name}'s with what's given changed: {title}, {prompt}, {sandbox} (sandbox, host or \"\"), {provider}, {model}, {effort}, {schedule}; {run} runs it after",
			Available: has, Run: func(ctx registry.Ctx, a registry.Args) error { _, err := x.save(ctx, a); return err }},
		{ID: "jobs.run", Title: "Run a job now: {name}, the selected one by default", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.runJob(ctx, a) }},
		{ID: "jobs.delete", Title: "Delete a job: {name}; its runs stay", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.deleteJob(ctx, a) }},
		{ID: "session.delete", Title: "Delete a session: {id}, or the one in front; its worktrees, workspace and log go, the repos stay", Available: has,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				id := core.SessionID(a.String("id"))
				if id == "" {
					id = h.State().Focus
				}
				c, err := x.sh.sessions.host()
				if err != nil {
					return err
				}
				return c.Delete(ctx, id)
			}},
		{ID: "jobs.form.save", Title: "Save the job the dialog holds; {run} runs it too", Available: formOpen,
			Run: func(_ registry.Ctx, a registry.Args) error { x.submit(a.Bool("run")); return nil }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-J>", Command: "jobs.open", Desc: "Jobs"}))
	for _, k := range []registry.Keymap{
		{Keys: "n", Command: "jobs.new", Desc: "New job"},
		{Keys: "e", Command: "jobs.edit", Desc: "Edit the job"},
		{Keys: "<D-CR>", Command: "jobs.run", Desc: "Run the job"},
	} {
		k.Mode = JobsKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: JobFormKeyMode, Keys: "<D-CR>", Command: "jobs.form.save", Args: registry.Args{"run": true},
		Desc: "Save and run"}))
	x.add(h.Contribute(registry.RailEntry{Name: "jobs", Icon: "jobs", Title: "Jobs", Command: "jobs.open", Scope: registry.ScopeGlobalEntry, Order: 30,
		Note: func(string) string { return x.figure() }, Active: func(string) bool { return x.open }}))
	x.add(h.Contribute(registry.StartEntry{Name: "jobs", Icon: "jobs", Title: "Jobs", Desc: "Saved prompts, their runs and schedules", Command: "jobs.open",
		Order: 80, Link: true, Note: x.count}))
	x.add(h.View(OverlayPrefix+"jobs", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "jobs", Fn: x.page} })))
	x.add(h.View(ConsoleSide+"10-job", registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "job", Fn: func(c *ui.Ctx) { x.consoleSide(c, core.SessionID(vc.Instance)) }}
	})))
	x.add(h.View(OverlayPrefix+"jobs.form", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "jobform", Fn: x.dialog} })))
	return nil
}

func (x *jobsPlugin) openPage(job string) error {
	x.open, x.err, x.confirm = true, "", ""
	x.refresh()
	if job != "" {
		for _, r := range x.rows() {
			if r.job.Name == job || r.job.Title == job {
				x.selKey = jobKey(r.job)
				return nil
			}
		}
		return fmt.Errorf("no job %q", job)
	}
	return nil
}

// refresh asks for the jobs again.
func (x *jobsPlugin) refresh() {
	x.mu.Lock()
	x.stale = true
	x.mu.Unlock()
	x.h.Redraw()
}

// load is the jobs and repos as last read, reading them again when they're old or missing.
func (x *jobsPlugin) load() ([]worker.Job, []core.Repo) {
	w := x.sh.cfg.Worker
	x.mu.Lock()
	defer x.mu.Unlock()
	old := x.stale || x.jobs.at.IsZero() || x.now().Sub(x.jobs.at) > refetchAfter
	if old && !x.inflight && w != nil {
		x.inflight, x.stale = true, false
		go x.fetch(w)
	}
	return x.jobs.v, x.repos
}

func (x *jobsPlugin) fetch(w *worker.Worker) {
	jobs, repos := w.Jobs(), w.Repos()
	x.h.Do(func() {
		x.mu.Lock()
		x.jobs, x.repos = fetched[[]worker.Job]{v: jobs, at: x.now()}, repos
		x.inflight = false
		x.mu.Unlock()
	})
	x.h.Redraw()
}

// busy says whether an answer is on its way.
func (x *jobsPlugin) busy() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.inflight || x.stale
}

// rows are the jobs with their runs, the one that ran last first and those never run last.
func (x *jobsPlugin) rows() []jobRow {
	jobs, _ := x.load()
	st := x.h.State()
	var out []jobRow
	for _, j := range jobs {
		r := jobRow{job: j}
		for _, se := range st.Sessions {
			if se.Job == j.Name {
				r.runs = append(r.runs, runRow{se: se, run: worker.RunOf(se)})
			}
		}
		slices.SortStableFunc(r.runs, func(a, b runRow) int { return b.se.Created.Compare(a.se.Created) })
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b jobRow) int {
		la, oka := a.last()
		lb, okb := b.last()
		switch {
		case oka && okb:
			if c := lb.se.Created.Compare(la.se.Created); c != 0 {
				return c
			}
		case oka != okb:
			if oka {
				return -1
			}
			return 1
		}
		if c := b.job.UpdatedAt.Compare(a.job.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(jobKey(a.job), jobKey(b.job))
	})
	return out
}

// selected is the selected job's row, else the first.
func (x *jobsPlugin) selected(rows []jobRow) (jobRow, int, bool) {
	for i, r := range rows {
		if jobKey(r.job) == x.selKey {
			return r, i, true
		}
	}
	if len(rows) > 0 {
		return rows[0], 0, true
	}
	return jobRow{}, -1, false
}

// target is the job a command names by {name}, else the selected one.
func (x *jobsPlugin) target(a registry.Args) (worker.Job, error) {
	rows := x.rows()
	name := a.String("name")
	if name == "" {
		if r, _, ok := x.selected(rows); ok {
			return r.job, nil
		}
		return worker.Job{}, errors.New("no job is selected")
	}
	for _, r := range rows {
		if r.job.Name == name {
			return r.job, nil
		}
	}
	return worker.Job{}, fmt.Errorf("no job %q", name)
}

func (x *jobsPlugin) client() (*worker.Worker, error) {
	if x.sh.sessions == nil {
		return nil, errors.New("there are no sessions in this window")
	}
	return x.sh.sessions.host()
}

// save is jobs.save: the job named, with what the args give changed, or a new one; it returns the job as saved.
func (x *jobsPlugin) save(ctx context.Context, a registry.Args) (worker.Job, error) {
	var j worker.Job
	if a.String("name") != "" {
		var err error
		if j, err = x.target(a); err != nil {
			return worker.Job{}, err
		}
	}
	for k, p := range map[string]*string{"title": &j.Title, "prompt": &j.Prompt, "sandbox": &j.Sandbox, "provider": &j.Provider,
		"model": &j.Model, "effort": &j.Effort} {
		if v, ok := a[k]; ok {
			*p = fmt.Sprint(v)
		}
	}
	if v, ok := a["schedule"]; ok {
		s, err := scheduleFrom(v)
		if err != nil {
			return worker.Job{}, err
		}
		j.Schedule = s
	}
	c, err := x.client()
	if err != nil {
		return worker.Job{}, err
	}
	saved, err := c.SaveJob(j)
	if err != nil {
		return worker.Job{}, err
	}
	x.h.Do(func() { x.selKey = jobKey(saved) })
	x.refresh()
	if a.Bool("run") {
		if _, err := c.RunJob(ctx, saved.Name); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

func (x *jobsPlugin) runJob(ctx context.Context, a registry.Args) error {
	j, err := x.target(a)
	if err != nil {
		return err
	}
	c, err := x.client()
	if err != nil {
		return err
	}
	_, err = c.RunJob(ctx, j.Name)
	return err
}

func (x *jobsPlugin) deleteJob(ctx context.Context, a registry.Args) error {
	j, err := x.target(a)
	if err != nil {
		return err
	}
	c, err := x.client()
	if err != nil {
		return err
	}
	if err := c.DeleteJob(j.Name); err != nil {
		return err
	}
	x.refresh()
	return nil
}

// do runs a command off the loop, as a click asks, and shows what went wrong on the page.
func (x *jobsPlugin) do(id string, a registry.Args) {
	go func() {
		err := x.h.Run(context.Background(), id, a)
		x.h.Do(func() {
			x.err = ""
			if err != nil {
				x.err = err.Error()
			}
		})
		x.h.Redraw()
	}()
}

// figure is the sidebar's note: how many runs are going.
func (x *jobsPlugin) figure() string {
	n := 0
	for _, se := range x.h.State().Sessions {
		if se.Job != "" && worker.RunOf(se).Status == worker.RunRunning {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d running", n)
}

// count is the start view's note: how many jobs there are.
func (x *jobsPlugin) count() string {
	jobs, _ := x.load()
	if len(jobs) == 0 {
		return ""
	}
	return counted(len(jobs), "job")
}

// days are the weekdays as schedules name them, Monday first.
var days = []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}

func dayName(d time.Weekday) string { return d.String()[:3] }

func parseDay(s string) (time.Weekday, bool) {
	for _, d := range days {
		if strings.EqualFold(s, dayName(d)) || strings.EqualFold(s, d.String()) {
			return d, true
		}
	}
	return 0, false
}

// scheduleFrom is a schedule from jobs.save's {schedule}: "manual", "weekdays 07:00", "weekly mon 07:00", "mon,thu
// 07:00", or a table of {every, days, at}.
func scheduleFrom(v any) (worker.Schedule, error) {
	var s worker.Schedule
	switch v := v.(type) {
	case nil:
		return s, nil
	case worker.Schedule:
		s = v
	case map[string]any:
		s.Every, _ = v["every"].(string)
		s.At, _ = v["at"].(string)
		var names []string
		switch ds := v["days"].(type) {
		case []any:
			for _, d := range ds {
				names = append(names, fmt.Sprint(d))
			}
		case []string:
			names = ds
		case string:
			names = strings.Split(ds, ",")
		}
		for _, n := range names {
			d, ok := parseDay(strings.TrimSpace(n))
			if !ok {
				return s, fmt.Errorf("no day %q", n)
			}
			s.Days = append(s.Days, d)
		}
	case string:
		f := strings.Fields(strings.ToLower(v))
		switch {
		case len(f) == 0 || f[0] == "manual":
			return s, nil
		case f[0] == "weekdays" && len(f) == 2:
			s.Every, s.At = worker.EveryWeekday, f[1]
		case f[0] == "weekly" && len(f) == 3:
			d, ok := parseDay(f[1])
			if !ok {
				return s, fmt.Errorf("no day %q", f[1])
			}
			s.Every, s.Days, s.At = worker.EveryWeek, []time.Weekday{d}, f[2]
		case len(f) == 2:
			s.Every, s.At = worker.EveryDays, f[1]
			for _, n := range strings.Split(f[0], ",") {
				d, ok := parseDay(n)
				if !ok {
					return s, fmt.Errorf("no day %q", n)
				}
				s.Days = append(s.Days, d)
			}
		default:
			return s, fmt.Errorf("a schedule is manual, weekdays 07:00, weekly mon 07:00 or mon,thu 07:00, not %q", v)
		}
	default:
		return s, fmt.Errorf("a schedule is a string or a table, not %T", v)
	}
	return s, s.Check()
}
