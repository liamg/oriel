package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// Job is a saved prompt with its pins (sessions.md → Jobs). It names no repo or branch: the prompt names the repos,
// and the agent running it adds them. Each is one file under the worker's jobs/.
type Job struct {
	Name     string `json:"name"` // its slug: the file, and what its runs are named after
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	Sandbox  string `json:"sandbox,omitempty"` // "sandbox", "host", or "" for the worker's default
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	// Schedule runs it by itself; the zero one is manual.
	Schedule  Schedule      `json:"schedule,omitzero"`
	Worker    core.WorkerID `json:"worker,omitempty"` // filled in as it's listed
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	// Skipped is the last time it was due while its last run was still going.
	Skipped time.Time `json:"skipped,omitzero"`
}

// How often a schedule runs a job.
const (
	EveryWeekday = "weekdays" // Monday to Friday
	EveryWeek    = "weekly"   // on Days[0]
	EveryDays    = "days"     // on each of Days: custom
)

// Schedule is when a job runs by itself, in the worker's local time. A run is skipped while the last is still going.
type Schedule struct {
	Every string         `json:"every,omitempty"` // "" for manual
	Days  []time.Weekday `json:"days,omitempty"`
	At    string         `json:"at,omitempty"` // "07:00"
}

func (s Schedule) clock() (int, int, error) {
	t, err := time.Parse("15:04", s.At)
	if err != nil {
		return 0, 0, fmt.Errorf("a schedule's time is HH:MM, not %q", s.At)
	}
	return t.Hour(), t.Minute(), nil
}

// Check says what's wrong with the schedule, or nil.
func (s Schedule) Check() error {
	switch s.Every {
	case "":
		return nil
	case EveryWeekday:
	case EveryWeek:
		if len(s.Days) != 1 {
			return errors.New("a weekly schedule names one day")
		}
	case EveryDays:
		if len(s.Days) == 0 {
			return errors.New("a custom schedule names its days")
		}
	default:
		return fmt.Errorf("a schedule is weekdays, weekly or days, not %q", s.Every)
	}
	_, _, err := s.clock()
	return err
}

func (s Schedule) on(d time.Weekday) bool {
	if s.Every == EveryWeekday {
		return d >= time.Monday && d <= time.Friday
	}
	return slices.Contains(s.Days, d)
}

// Next is when the schedule next fires after t, in t's zone; zero for a manual one.
func (s Schedule) Next(t time.Time) time.Time {
	if s.Every == "" || s.Check() != nil {
		return time.Time{}
	}
	h, m, _ := s.clock()
	for i := range 8 {
		d := t.AddDate(0, 0, i)
		at := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, t.Location())
		if at.After(t) && s.on(at.Weekday()) {
			return at
		}
	}
	return time.Time{}
}

// String is how the schedule reads: "manual", "every weekday 07:00", "weekly Mon 07:00", "Mon, Thu 07:00".
func (s Schedule) String() string {
	var days []string
	for _, d := range s.Days {
		days = append(days, d.String()[:3])
	}
	switch s.Every {
	case EveryWeekday:
		return "every weekday " + s.At
	case EveryWeek:
		return "weekly " + strings.Join(days, ", ") + " " + s.At
	case EveryDays:
		return strings.Join(days, ", ") + " " + s.At
	}
	return "manual"
}

func (w *Worker) jobsDir() string { return filepath.Join(w.opts.Home, "jobs") }

func (w *Worker) jobPath(name string) string { return filepath.Join(w.jobsDir(), name+".json") }

func (w *Worker) loadJob(name string) (Job, bool) {
	b, err := os.ReadFile(w.jobPath(name))
	if err != nil {
		return Job{}, false
	}
	var j Job
	if json.Unmarshal(b, &j) != nil || j.Prompt == "" {
		return Job{}, false
	}
	j.Worker = w.opts.ID
	return j, true
}

func (w *Worker) writeJob(j Job) error {
	if err := os.MkdirAll(w.jobsDir(), 0o700); err != nil {
		return err
	}
	j.Worker = ""
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	tmp := w.jobPath(j.Name) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.jobPath(j.Name))
}

// Jobs is every job, the most recently changed first.
func (w *Worker) Jobs() []Job {
	w.jobsMu.Lock()
	defer w.jobsMu.Unlock()
	return w.jobs()
}

func (w *Worker) jobs() []Job {
	entries, err := os.ReadDir(w.jobsDir())
	if err != nil {
		return nil
	}
	var out []Job
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			if j, ok := w.loadJob(name); ok {
				out = append(out, j)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Job) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// SaveJob writes a job: a new one when j has no Name (named from its title, stepping aside from a name in use), else
// the job of that name, rewritten.
func (w *Worker) SaveJob(j Job) (Job, error) {
	j.Prompt, j.Title = strings.TrimSpace(j.Prompt), strings.TrimSpace(j.Title)
	if j.Prompt == "" {
		return Job{}, errors.New("a job is a prompt: say what it should do")
	}
	if err := j.Schedule.Check(); err != nil {
		return Job{}, err
	}
	switch j.Sandbox {
	case "", "sandbox", "host":
	default:
		return Job{}, fmt.Errorf("a job runs in a sandbox or on the host, not %q", j.Sandbox)
	}
	if j.Title == "" {
		j.Title = promptTitle(j.Prompt)
	}
	w.jobsMu.Lock()
	defer w.jobsMu.Unlock()
	now := w.opts.Now().UTC()
	j.UpdatedAt = now
	if name := Sanitise(j.Name); name != "" {
		old, ok := w.loadJob(name)
		if !ok {
			return Job{}, fmt.Errorf("no job %q", name)
		}
		j.Name, j.CreatedAt, j.Skipped = name, old.CreatedAt, old.Skipped
	} else {
		base := trimWords(Sanitise(j.Title), 6)
		if base == "" {
			base = "job"
		}
		j.Name, j.CreatedAt = base, now
		for n := 2; ; n++ {
			if _, err := os.Stat(w.jobPath(j.Name)); err != nil {
				break
			}
			j.Name = fmt.Sprintf("%s-%d", base, n)
		}
	}
	if err := w.writeJob(j); err != nil {
		return Job{}, err
	}
	j.Worker = w.opts.ID
	return j, nil
}

// DeleteJob forgets a job. Its runs are sessions of their own, and stay.
func (w *Worker) DeleteJob(name string) error {
	w.jobsMu.Lock()
	defer w.jobsMu.Unlock()
	name = Sanitise(name)
	if _, ok := w.loadJob(name); !ok {
		return fmt.Errorf("no job %q", name)
	}
	return os.Remove(w.jobPath(name))
}

// promptTitle is a prompt's first line, without heading marks, cut to fit a row.
func promptTitle(prompt string) string {
	line := strings.TrimSpace(strings.TrimLeft(firstLine(prompt), "#> -*"))
	if r := []rune(line); len(r) > 80 {
		line = strings.TrimSpace(string(r[:80])) + "…"
	}
	return line
}

// RunJob starts a run of a job: a session of kind job on its pins.
func (w *Worker) RunJob(ctx context.Context, name string) (core.SessionID, error) {
	w.jobsMu.Lock()
	j, ok := w.loadJob(Sanitise(name))
	w.jobsMu.Unlock()
	if !ok {
		return "", fmt.Errorf("no job %q", name)
	}
	return w.runJob(ctx, j)
}

func (w *Worker) runJob(ctx context.Context, j Job) (core.SessionID, error) {
	provider := j.Provider
	if provider == "" {
		if ps := w.opts.Providers.List(); len(ps) > 0 {
			provider = ps[0].ID()
		}
	}
	sandboxed := j.Sandbox == "sandbox"
	if j.Sandbox == "" {
		w.mu.Lock()
		sandboxed = w.opts.Sandbox != nil && w.sandboxCfg.Default != "host"
		w.mu.Unlock()
	}
	spec := core.SessionSpec{Kind: core.KindJob, Name: trimWords(j.Name, 3), Prompt: j.Prompt, Provider: provider, Model: j.Model, Effort: j.Effort,
		Sandbox: core.SandboxPolicy{Container: sandboxed}, Job: j.Name}
	return w.Start(ctx, spec)
}

// RunDue starts every scheduled job due since it last looked, skipping one whose last run is still going.
func (w *Worker) RunDue(ctx context.Context) {
	w.jobsMu.Lock()
	now := w.opts.Now()
	last := w.lastDue
	w.lastDue = now
	var due []Job
	for _, j := range w.jobs() {
		at := j.Schedule.Next(last.In(now.Location()))
		if at.IsZero() || at.After(now) {
			continue
		}
		if w.running(j.Name) {
			j.Skipped = at.UTC()
			if err := w.writeJob(j); err != nil {
				w.log.Warn("job", "job", j.Name, "err", err)
			}
			w.log.Info("scheduled run skipped: the last is still going", "job", j.Name)
			continue
		}
		due = append(due, j)
	}
	w.jobsMu.Unlock()
	for _, j := range due {
		if _, err := w.runJob(ctx, j); err != nil {
			w.log.Warn("scheduled run", "job", j.Name, "err", err)
		}
	}
}

// running says whether a run of the job is still going.
func (w *Worker) running(job string) bool {
	return slices.ContainsFunc(w.store.State().Sessions, func(se core.SessionState) bool {
		return se.Job == job && !se.Archived && RunOf(se).Status == RunRunning
	})
}

// RunSchedules runs the jobs' schedules every interval until ctx ends: while the worker is awake.
func (w *Worker) RunSchedules(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.closed:
			return
		case <-t.C:
			w.RunDue(ctx)
		}
	}
}

// A run's status (ui.md 44).
const (
	RunRunning = "running"
	RunDone    = "done"
	RunBlocked = "blocked"
	RunStopped = "stopped"
)

// Run is a job session as its row shows it.
type Run struct {
	Status  string
	Summary string // the agent's last word on the request: its answer, or how it's getting on
	Repos   []string
	Started time.Time
	Ended   time.Time // the agent's last word; zero while it runs
}

// Took is how long it ran: to its last answer, or zero while it runs.
func (r Run) Took() time.Duration {
	if r.Ended.IsZero() || r.Started.IsZero() {
		return 0
	}
	return r.Ended.Sub(r.Started)
}

// blockedMark starts a blocked run's answer (the job pack's).
const blockedMark = "**Blocked:**"

// RunOf reads a job session's run from its request thread: done once it's resolved, blocked while the agent's answer
// waits on you, running while the agent has it, and stopped when it was stopped before either.
func RunOf(se core.SessionState) Run {
	r := Run{Started: se.Created}
	for _, p := range se.Projects {
		r.Repos = append(r.Repos, p.Name)
	}
	i := slices.IndexFunc(se.Threads, func(t core.Thread) bool { return !t.Btw })
	var t core.Thread
	if i >= 0 {
		t = se.Threads[i]
	}
	for _, c := range slices.Backward(t.Comments) {
		if c.Author == core.AuthorAgent {
			r.Summary = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(c.Body), blockedMark)), " ")
			r.Ended = c.Time
			break
		}
	}
	asking := slices.ContainsFunc(se.Asks, func(a core.Ask) bool { return a.Kind == core.AskQuestion })
	switch {
	case i >= 0 && !t.Open():
		r.Status = RunDone
	case asking || i >= 0 && t.AgentAnswered():
		r.Status = RunBlocked
		if asking && !t.AgentAnswered() {
			r.Ended, r.Summary = time.Time{}, askText(se.Asks)
		}
	case se.Agent.Running && !se.Archived:
		r.Status, r.Ended = RunRunning, time.Time{}
	default:
		r.Status = RunStopped
	}
	return r
}

// askText is the agent's open question.
func askText(asks []core.Ask) string {
	for _, a := range asks {
		if a.Kind == core.AskQuestion {
			if len(a.Questions) > 0 {
				return a.Questions[0].Question
			}
			return a.Summary
		}
	}
	return ""
}
