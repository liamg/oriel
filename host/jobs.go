package host

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
)

// SaveJob is jobs.Jobs.SaveJob, with a panic recovered.
func (w *Host) SaveJob(j Job) (_ Job, err error) {
	defer w.recover("SaveJob", &err)
	return w.jobs.SaveJob(j)
}

// DeleteJob is jobs.Jobs.DeleteJob, with a panic recovered.
func (w *Host) DeleteJob(name string) (err error) {
	defer w.recover("DeleteJob", &err)
	return w.jobs.DeleteJob(name)
}

// RunJob is jobs.Jobs.RunJob, with a panic recovered.
func (w *Host) RunJob(ctx context.Context, name string) (_ core.SessionID, err error) {
	defer w.recover("RunJob", &err)
	return w.jobs.RunJob(ctx, name)
}

// Jobs is jobs.Jobs.Jobs, with a panic recovered.
func (w *Host) Jobs() []Job {
	defer w.recover("Jobs", nil)
	return w.jobs.Jobs()
}

// RunDue is jobs.Jobs.RunDue, with a panic recovered.
func (w *Host) RunDue(ctx context.Context) {
	defer w.recover("RunDue", nil)
	w.jobs.RunDue(ctx)
}

// RunSchedules is jobs.Jobs.RunSchedules, with a panic recovered.
func (w *Host) RunSchedules(ctx context.Context, every time.Duration) {
	defer w.recover("RunSchedules", nil)
	w.jobs.RunSchedules(ctx, every)
}
