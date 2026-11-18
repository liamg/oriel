package host

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
)

// Connect sets the app's tools and answers and its kinds before its providers, whose setting resumes the agents
// handed over: they come back with all of it in place.
func (w *Host) Connect(c hostapi.Connection) {
	w.setApp(c.App)
	w.setKinds(c.Kinds)
	w.setProviders(c.Providers)
}

func (w *Host) setApp(a hostapi.App) {
	defer w.env.Recover("SetApp", nil)
	w.run.SetApp(a)
}

// EditQueued changes or deletes a comment of the user's the agent hasn't been given yet.
func (w *Host) EditQueued(r hostapi.QueuedReq) (err error) {
	defer w.env.Recover("EditQueued", &err)
	err = w.run.EditQueued(r)
	w.restatus(r.Session)
	return err
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
