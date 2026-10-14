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
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/worker"
)

// Jobs (SE-4, mockup 44): saved prompts on each worker, each run a session of kind job; the Jobs page lists them with
// their runs and schedules, built on what every worker answers (jobs.list, jobs.save, jobs.run, session.delete).
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
)

func runTone(status string) theme.TokenID {
	switch status {
	case worker.RunRunning:
		return tRunRunning
	case worker.RunDone:
		return tRunDone
	case worker.RunBlocked:
		return tRunBlocked
	}
	return tRunStopped
}

// runFilters are the runs card's filters: all, then each status.
var runFilters = []string{"", worker.RunRunning, worker.RunDone, worker.RunBlocked, worker.RunStopped}

type jobsExt struct {
	disposer
	sh  *shell
	h   ext.Host
	now func() time.Time
	loc *time.Location

	open      bool
	selKey    string // the selected job: its worker and name
	runFilter int    // into runFilters
	filter    string // the job list's filter
	runSel    int    // the runs list's selection
	confirm   string // what a Delete asks "Sure?" about: a job's key or a run's session
	err       string

	form *jobForm // the New job and Edit dialog, while it's open

	mu       sync.Mutex
	jobs     map[core.WorkerID]fetched[[]worker.Job]
	repos    map[core.WorkerID][]core.Repo
	inflight bool
	stale    bool
}

// jobRow is a job with its runs, newest first.
type jobRow struct {
	job  worker.Job
	runs []runRow
}

type runRow struct {
	se  core.SessionState
	run worker.Run
}

func jobKey(j worker.Job) string { return string(j.Worker) + "/" + j.Name }

func (r jobRow) last() (runRow, bool) {
	if len(r.runs) == 0 {
		return runRow{}, false
	}
	return r.runs[0], true
}

func (x *jobsExt) Manifest() ext.Manifest { return ext.Manifest{ID: "jobs"} }

func (x *jobsExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	if x.now == nil {
		x.now = time.Now
	}
	if x.loc == nil {
		x.loc = time.Local
	}
	x.jobs = map[core.WorkerID]fetched[[]worker.Job]{}
	x.repos = map[core.WorkerID][]core.Repo{}
	workers := func(registry.Args) bool { return x.sh.sessions != nil && len(x.sh.sessions.clients()) > 0 }
	formOpen := func(registry.Args) bool { return x.form != nil }
	for _, c := range []registry.Command{
		{ID: "Jobs", Title: "The Jobs page; :Jobs {job}", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(strings.TrimSpace(a.String("args")))
		}},
		{ID: "jobs.open", Title: "Open the Jobs page, at a {job} by name", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("job"))
		}},
		{ID: "jobs.new", Title: "New job: the dialog, with {title}, {prompt}, {sandbox}, {provider}, {model}, {effort}, {worker} or {schedule} filled in",
			Available: workers, Run: func(_ registry.Ctx, a registry.Args) error { x.newForm(a); return nil }},
		{ID: "jobs.edit", Title: "Edit a job in the dialog: {name} on {worker}, the selected one by default", Available: workers,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.editForm(a, false) }},
		{ID: "jobs.duplicate", Title: "Duplicate a job in the New job dialog: {name} on {worker}, the selected one by default", Available: workers,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.editForm(a, true) }},
		{ID: "jobs.save", Title: "Save a job on {worker}: a new one, or {name}'s with what's given changed: {title}, {prompt}, {sandbox} (sandbox, host or \"\"), {provider}, {model}, {effort}, {schedule}; {run} runs it after",
			Available: workers, Run: func(ctx registry.Ctx, a registry.Args) error { _, err := x.save(ctx, a); return err }},
		{ID: "jobs.run", Title: "Run a job now: {name} on {worker}, the selected one by default", Available: workers,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.runJob(ctx, a) }},
		{ID: "jobs.delete", Title: "Delete a job: {name} on {worker}; its runs stay", Available: workers,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.deleteJob(ctx, a) }},
		{ID: "session.delete", Title: "Delete a session: {id}, or the one in front; its worktrees, workspace and log go, the repos stay", Available: workers,
			Run: func(ctx registry.Ctx, a registry.Args) error {
				id := core.SessionID(a.String("id"))
				if id == "" {
					id = h.State().Focus
				}
				c, err := x.sh.sessions.client(id)
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

func (x *jobsExt) openPage(job string) error {
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

// refresh asks the workers for their jobs again.
func (x *jobsExt) refresh() {
	x.mu.Lock()
	x.stale = true
	x.mu.Unlock()
	x.h.Redraw()
}

// load is every worker's jobs and repos as last answered, asking again when they're old or missing.
func (x *jobsExt) load() (map[core.WorkerID][]worker.Job, map[core.WorkerID][]core.Repo) {
	var clients map[core.WorkerID]worker.Client
	if x.sh.sessions != nil {
		clients = x.sh.sessions.byWorker()
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	out := map[core.WorkerID][]worker.Job{}
	old := x.stale
	for id, f := range x.jobs {
		if _, live := clients[id]; !live {
			continue
		}
		out[id] = f.v
		if x.now().Sub(f.at) > refetchAfter {
			old = true
		}
	}
	for id := range clients {
		if _, ok := x.jobs[id]; !ok {
			old = true
		}
	}
	repos := map[core.WorkerID][]core.Repo{}
	for id, r := range x.repos {
		repos[id] = r
	}
	if old && !x.inflight && len(clients) > 0 {
		x.inflight, x.stale = true, false
		go x.fetch(clients)
	}
	return out, repos
}

func (x *jobsExt) fetch(clients map[core.WorkerID]worker.Client) {
	type answer struct {
		jobs  []worker.Job
		repos []core.Repo
		err   error
	}
	got := map[core.WorkerID]answer{}
	for id, c := range clients {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		var a answer
		if a.jobs, a.err = c.Jobs(ctx); a.err == nil {
			a.repos, _ = c.Repos(ctx)
		}
		cancel()
		got[id] = a
	}
	x.h.Do(func() {
		x.mu.Lock()
		at := x.now()
		for id, a := range got {
			if a.err != nil {
				x.h.Log().Warn("jobs", "worker", id, "err", a.err)
				x.jobs[id] = fetched[[]worker.Job]{v: x.jobs[id].v, at: at, err: a.err}
				continue
			}
			x.jobs[id] = fetched[[]worker.Job]{v: a.jobs, at: at}
			x.repos[id] = a.repos
		}
		x.inflight = false
		x.mu.Unlock()
	})
	x.h.Redraw()
}

// busy says whether an answer is on its way.
func (x *jobsExt) busy() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.inflight || x.stale
}

// rows are every worker's jobs with their runs, the one that ran last first and those never run last.
func (x *jobsExt) rows() []jobRow {
	all, _ := x.load()
	st := x.h.State()
	var out []jobRow
	for wid, jobs := range all {
		for _, j := range jobs {
			r := jobRow{job: j}
			for _, se := range st.Sessions {
				if se.Job == j.Name && cmpOr(string(se.Worker), string(core.LocalWorker)) == string(wid) {
					r.runs = append(r.runs, runRow{se: se, run: worker.RunOf(se)})
				}
			}
			slices.SortStableFunc(r.runs, func(a, b runRow) int { return b.se.Created.Compare(a.se.Created) })
			out = append(out, r)
		}
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
func (x *jobsExt) selected(rows []jobRow) (jobRow, int, bool) {
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

// target is the job a command names by {name} and {worker}, else the selected one.
func (x *jobsExt) target(a registry.Args) (worker.Job, error) {
	rows := x.rows()
	name, wid := a.String("name"), a.String("worker")
	if name == "" {
		if r, _, ok := x.selected(rows); ok {
			return r.job, nil
		}
		return worker.Job{}, errors.New("no job is selected")
	}
	for _, r := range rows {
		if r.job.Name == name && (wid == "" || string(r.job.Worker) == wid) {
			return r.job, nil
		}
	}
	return worker.Job{}, fmt.Errorf("no job %q", name)
}

func (x *jobsExt) client(wid core.WorkerID) (worker.Client, error) {
	if x.sh.sessions == nil {
		return worker.Client{}, errors.New("no worker is connected")
	}
	return x.sh.sessions.worker(wid)
}

// save is jobs.save: the job named, with what the args give changed, or a new one; it returns the job as saved.
func (x *jobsExt) save(ctx context.Context, a registry.Args) (worker.Job, error) {
	j := worker.Job{Worker: core.WorkerID(a.String("worker"))}
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
	c, err := x.client(j.Worker)
	if err != nil {
		return worker.Job{}, err
	}
	saved, err := c.SaveJob(ctx, j)
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

func (x *jobsExt) runJob(ctx context.Context, a registry.Args) error {
	j, err := x.target(a)
	if err != nil {
		return err
	}
	c, err := x.client(j.Worker)
	if err != nil {
		return err
	}
	_, err = c.RunJob(ctx, j.Name)
	return err
}

func (x *jobsExt) deleteJob(ctx context.Context, a registry.Args) error {
	j, err := x.target(a)
	if err != nil {
		return err
	}
	c, err := x.client(j.Worker)
	if err != nil {
		return err
	}
	if err := c.DeleteJob(ctx, j.Name); err != nil {
		return err
	}
	x.refresh()
	return nil
}

// do runs a command off the loop, as a click asks, and shows what went wrong on the page.
func (x *jobsExt) do(id string, a registry.Args) {
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
func (x *jobsExt) figure() string {
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
func (x *jobsExt) count() string {
	all, _ := x.load()
	n := 0
	for _, js := range all {
		n += len(js)
	}
	if n == 0 {
		return ""
	}
	return counted(n, "job")
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
