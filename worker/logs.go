package worker

import (
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
)

// Logs is each session's log of notices, kept with the session.
func (w *Worker) Logs() logs.SessionLogs { return noticeLogs{w} }

type noticeLogs struct{ w *Worker }

func (s noticeLogs) Open(id core.SessionID) (logs.SessionLog, error) { return noticeLog{s.w, id}, nil }

type noticeLog struct {
	w  *Worker
	id core.SessionID
}

func (l noticeLog) Append(rec logs.Record) (err error) {
	defer l.w.recover("a session log", &err)
	sl, err := l.w.openSessionLog(l.id)
	if err != nil {
		return err
	}
	return sl.Append(rec)
}

func (l noticeLog) Records() (_ []logs.Record, err error) {
	defer l.w.recover("a session log", &err)
	sl, err := l.w.openSessionLog(l.id)
	if err != nil {
		return nil, err
	}
	return sl.Records()
}

func (l noticeLog) Close() error { return nil }

// openSessionLog is a session's log, opened once and kept until the host closes.
func (w *Worker) openSessionLog(id core.SessionID) (logs.SessionLog, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if l := w.open[id]; l != nil {
		return l, nil
	}
	l, err := w.sessLogs.Open(id)
	if err != nil {
		return nil, err
	}
	w.open[id] = l
	return l, nil
}
