package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
)

// SaveJob writes a job: a new one when j has no Name (named from its title, stepping aside from a name in use), else
// the job of that name, rewritten.
func (w *Host) SaveJob(j Job) (_ Job, err error) {
	defer w.recover("SaveJob", &err)
	return w.jobs.SaveJob(j)
}

// DeleteJob forgets a job. Its runs are sessions of their own, and stay.
func (w *Host) DeleteJob(name string) (err error) {
	defer w.recover("DeleteJob", &err)
	return w.jobs.DeleteJob(name)
}

// RunJob starts a run of a job: a session of kind job on its pins.
func (w *Host) RunJob(ctx context.Context, name string) (_ core.SessionID, err error) {
	defer w.recover("RunJob", &err)
	return w.jobs.RunJob(ctx, name)
}

// Jobs is every job, the most recently changed first.
func (w *Host) Jobs() []Job {
	defer w.recover("Jobs", nil)
	return w.jobs.Jobs()
}

// RunDue starts every scheduled job due since its last occurrence (or since it was saved), once however many it
// missed while the host was off, skipping one whose last run is still going.
func (w *Host) RunDue(ctx context.Context) {
	defer w.recover("RunDue", nil)
	w.jobs.RunDue(ctx)
}

// RunSchedules runs the jobs' schedules every interval until ctx ends: while the host is awake.
func (w *Host) RunSchedules(ctx context.Context, every time.Duration) {
	defer w.recover("RunSchedules", nil)
	w.jobs.RunSchedules(ctx, every)
}
