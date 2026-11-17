//oriel:kind job

// Package jobs is saved prompts the user runs as sessions of their own, by hand or on a schedule.
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/internal/atomicfile"
)

// Jobs are the saved jobs, each a file under the host's jobs/, and their schedules.
type Jobs struct {
	env  hostcore.Env
	st   hostcore.State
	deps Deps
	mu   sync.Mutex // the jobs' files, and when the schedules last ran
}

// Deps are what running a job needs of the rest of the host.
type Deps struct {
	Providers *agent.Providers
	// Sandboxed says a job with no sandbox of its own runs in one (sandbox.default).
	Sandboxed func() bool
	// Start starts a session.
	Start func(ctx context.Context, spec core.SessionSpec) (core.SessionID, error)
}

// New is the jobs under env's home.
func New(env hostcore.Env, st hostcore.State, deps Deps) *Jobs {
	return &Jobs{env: env, st: st, deps: deps}
}

// maxSkips is how many skipped occurrences a job keeps.
const maxSkips = 50

func (x *Jobs) jobsDir() string { return filepath.Join(x.env.Home, "jobs") }

func (x *Jobs) jobPath(name string) string { return filepath.Join(x.jobsDir(), name+".json") }

func (x *Jobs) loadJob(name string) (hostapi.Job, bool) {
	b, err := os.ReadFile(x.jobPath(name))
	if err != nil {
		return hostapi.Job{}, false
	}
	var j hostapi.Job
	if json.Unmarshal(b, &j) != nil || j.Prompt == "" {
		return hostapi.Job{}, false
	}
	return j, true
}

func (x *Jobs) writeJob(j hostapi.Job) error {
	if err := os.MkdirAll(x.jobsDir(), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(x.jobPath(j.Name), b, 0o600)
}

// Jobs is every job, the most recently changed first.
func (x *Jobs) Jobs() []hostapi.Job {
	defer x.env.Recover("Jobs", nil)
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.jobs()
}

func (x *Jobs) jobs() []hostapi.Job {
	entries, err := os.ReadDir(x.jobsDir())
	if err != nil {
		return nil
	}
	var out []hostapi.Job
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			if j, ok := x.loadJob(name); ok {
				out = append(out, j)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b hostapi.Job) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// SaveJob writes a job: a new one when j has no Name (named from its title, stepping aside from a name in use), else
// the job of that name, rewritten.
func (x *Jobs) SaveJob(j hostapi.Job) (_ hostapi.Job, err error) {
	defer x.env.Recover("SaveJob", &err)
	j.Prompt, j.Title = strings.TrimSpace(j.Prompt), strings.TrimSpace(j.Title)
	if j.Prompt == "" {
		return hostapi.Job{}, errors.New("a job is a prompt: say what it should do")
	}
	if err := j.Schedule.Check(); err != nil {
		return hostapi.Job{}, err
	}
	switch j.Sandbox {
	case "", "sandbox", "host":
	default:
		return hostapi.Job{}, fmt.Errorf("a job runs in a sandbox or on the host, not %q", j.Sandbox)
	}
	if j.Title == "" {
		j.Title = promptTitle(j.Prompt)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	now := x.env.Now().UTC()
	j.UpdatedAt = now
	if name := hostapi.Sanitise(j.Name); name != "" {
		old, ok := x.loadJob(name)
		if !ok {
			return hostapi.Job{}, fmt.Errorf("no job %q", name)
		}
		j.Name, j.CreatedAt, j.Due, j.Skips = name, old.CreatedAt, old.Due, old.Skips
	} else {
		base := hostcore.TrimWords(hostapi.Sanitise(j.Title), 6)
		if base == "" {
			base = "job"
		}
		j.Name, j.CreatedAt = base, now
		for n := 2; ; n++ {
			if _, err := os.Stat(x.jobPath(j.Name)); err != nil {
				break
			}
			j.Name = fmt.Sprintf("%s-%d", base, n)
		}
	}
	if err := x.writeJob(j); err != nil {
		return hostapi.Job{}, err
	}
	return j, nil
}

// DeleteJob forgets a job. Its runs are sessions of their own, and stay.
func (x *Jobs) DeleteJob(name string) (err error) {
	defer x.env.Recover("DeleteJob", &err)
	x.mu.Lock()
	defer x.mu.Unlock()
	name = hostapi.Sanitise(name)
	if _, ok := x.loadJob(name); !ok {
		return fmt.Errorf("no job %q", name)
	}
	return os.Remove(x.jobPath(name))
}

// promptTitle is a prompt's first line, without heading marks, cut to fit a row.
func promptTitle(prompt string) string {
	line := strings.TrimSpace(strings.TrimLeft(core.Summary(prompt), "-*"))
	if r := []rune(line); len(r) > 80 {
		line = strings.TrimSpace(string(r[:80])) + "…"
	}
	return line
}

// RunJob starts a run of a job: a session of kind job on its pins.
func (x *Jobs) RunJob(ctx context.Context, name string) (_ core.SessionID, err error) {
	defer x.env.Recover("RunJob", &err)
	x.mu.Lock()
	j, ok := x.loadJob(hostapi.Sanitise(name))
	x.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no job %q", name)
	}
	return x.runJob(ctx, j)
}

func (x *Jobs) runJob(ctx context.Context, j hostapi.Job) (core.SessionID, error) {
	provider := j.Provider
	if provider == "" {
		if ps := x.deps.Providers.List(); len(ps) > 0 {
			provider = ps[0].ID()
		}
	}
	sandboxed := j.Sandbox == "sandbox"
	if j.Sandbox == "" {
		sandboxed = x.deps.Sandboxed()
	}
	name := hostcore.TrimWords(j.Name, 3)
	spec := core.SessionSpec{Kind: core.KindJob, Name: name, Title: "job: " + strings.ReplaceAll(name, "-", " "), Prompt: j.Prompt,
		Provider: provider, Model: j.Model, Effort: j.Effort, Sandbox: core.SandboxPolicy{Container: sandboxed}, Job: j.Name}
	return x.deps.Start(ctx, spec)
}

// RunDue starts every scheduled job due since its last occurrence (or since it was saved), once however many it
// missed while the host was off, skipping one whose last run is still going.
func (x *Jobs) RunDue(ctx context.Context) {
	defer x.env.Recover("RunDue", nil)
	x.mu.Lock()
	now := x.env.Now()
	var due []hostapi.Job
	for _, j := range x.jobs() {
		from := j.UpdatedAt
		if j.Due.After(from) {
			from = j.Due
		}
		at := j.Schedule.Last(from.In(now.Location()), now)
		if at.IsZero() {
			continue
		}
		j.Due = at.UTC()
		if x.running(j.Name) {
			j.Skips = append(j.Skips, j.Due)
			j.Skips = j.Skips[max(0, len(j.Skips)-maxSkips):]
			x.env.Log.Info("scheduled run skipped: the last is still going", "job", j.Name)
		} else {
			due = append(due, j)
		}
		if err := x.writeJob(j); err != nil {
			x.env.Log.Warn("job", "job", j.Name, "err", err)
		}
	}
	x.mu.Unlock()
	for _, j := range due {
		if _, err := x.runJob(ctx, j); err != nil {
			x.env.Log.Warn("scheduled run", "job", j.Name, "err", err)
		}
	}
}

// running says whether a run of the job is still going.
func (x *Jobs) running(job string) bool {
	return slices.ContainsFunc(x.st.Snapshot().Sessions, func(se core.SessionState) bool {
		return hostapi.JobOf(se) == job && !se.Archived && hostapi.RunOf(se).Status == hostapi.RunRunning
	})
}

// RunSchedules runs the jobs' schedules every interval until ctx ends: while the host is awake.
func (x *Jobs) RunSchedules(ctx context.Context, every time.Duration) {
	defer x.env.Recover("RunSchedules", nil)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-x.env.Closed:
			return
		case <-t.C:
			func() {
				defer x.env.Recover("RunDue", nil)
				x.RunDue(ctx)
			}()
		}
	}
}
