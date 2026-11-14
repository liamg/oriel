package host

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// SetApp is agentrun.Runner.SetApp.
func (w *Host) SetApp(a App) { w.runPart.SetApp(a) }

// EditQueued changes or deletes a comment of the user's the agent hasn't been given yet.
func (w *Host) EditQueued(r QueuedReq) (err error) {
	defer w.env.Recover("EditQueued", &err)
	err = w.runPart.EditQueued(r)
	w.restatus(r.Session)
	return err
}

// RecordRun keeps a test run in its session, and returns its ID.
func (w *Host) RecordRun(id core.SessionID, run core.TestRun) (_ string, err error) {
	defer w.env.Recover("RecordRun", &err)
	if run.ID == "" {
		run.ID = hostcore.NewID(4)
	}
	return run.ID, w.emit(core.TestRunRecorded{Session: id, Run: run})
}

// SetReviewed marks a file's changes reviewed, or takes the marks off.
func (w *Host) SetReviewed(r core.HunksReviewed) (err error) {
	defer w.env.Recover("SetReviewed", &err)
	return w.emit(r)
}

// RecordVerified keeps a verify quiz's result in the session.
func (w *Host) RecordVerified(v core.Verified) (err error) {
	defer w.env.Recover("RecordVerified", &err)
	return w.emit(v)
}
