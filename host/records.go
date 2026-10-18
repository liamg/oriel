package host

import "github.com/liamg/oriel/core"

// RecordRun keeps a test run in its session, and returns its ID.
func (w *Host) RecordRun(id core.SessionID, run core.TestRun) (_ string, err error) {
	defer w.recover("RecordRun", &err)
	if run.ID == "" {
		run.ID = newID(4)
	}
	return run.ID, w.emit(core.TestRunRecorded{Session: id, Run: run})
}

// SetReviewed marks a file's changes reviewed, or takes the marks off.
func (w *Host) SetReviewed(r core.HunksReviewed) (err error) {
	defer w.recover("SetReviewed", &err)
	return w.emit(r)
}

// SetSeen records lines of a file's change seen in the diff.
func (w *Host) SetSeen(r core.LinesSeen) (err error) {
	defer w.recover("SetSeen", &err)
	return w.emit(r)
}

// RecordOverride records a commit made past the commit gate.
func (w *Host) RecordOverride(r core.CommitGateOverridden) (err error) {
	defer w.recover("RecordOverride", &err)
	return w.emit(r)
}

// RecordVerified keeps a verify quiz's result in the session.
func (w *Host) RecordVerified(v core.Verified) (err error) {
	defer w.recover("RecordVerified", &err)
	return w.emit(v)
}
