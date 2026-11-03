// Package persist keeps the session host's state on disk: each session's event log (restored on start, compacted when
// torn), the notices each session logs, and the stats, usage and transcripts read back from what's kept.
package persist

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host/hostcore"
)

// Logs are the sessions' event logs and notice logs.
type Logs struct {
	env hostcore.Env
	st  hostcore.State

	mu       sync.Mutex
	recs     map[core.SessionID]*sessionLog
	unlogged map[core.SessionID]bool // sessions whose log failed: their events are no longer recorded
	open     map[core.SessionID]logs.SessionLog
	notices  logs.Dirs
}

type sessionLog struct {
	f   *os.File
	rec *core.Recorder
}

// NewLogs keeps the logs under env's home.
func NewLogs(env hostcore.Env, st hostcore.State) *Logs {
	return &Logs{env: env, st: st, recs: map[core.SessionID]*sessionLog{}, unlogged: map[core.SessionID]bool{},
		open: map[core.SessionID]logs.SessionLog{}, notices: logs.Dirs{Root: filepath.Join(env.Home, "sessions")}}
}

func (l *Logs) eventsPath(id core.SessionID) string {
	return filepath.Join(l.env.SessionDir(id), "events.jsonl")
}

// Restore replays each session's log into store, dropping crit's workspaces. A log that stops early (a torn last
// line) keeps what it had.
func (l *Logs) Restore(store core.Store) error {
	entries, err := os.ReadDir(filepath.Join(l.env.Home, "sessions"))
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := core.SessionID(e.Name())
		f, err := os.Open(l.eventsPath(id))
		if err != nil {
			continue
		}
		n, err := core.ReplayVault(bufio.NewReader(f), store, l.vault(id))
		f.Close()
		if se, ok := store.State().Session(id); ok && se.Snapshot().Source == "crit" {
			_ = store.Apply(core.SessionClosed{ID: id})
			_ = os.RemoveAll(l.env.SessionDir(id))
			continue
		}
		if err != nil {
			l.env.Log.Warn("session log stops early", "session", id, "after", n, "err", err)
			if err := l.compact(store, id); err != nil {
				return err
			}
			continue
		}
		if err := l.openLog(id, n); err != nil {
			return err
		}
	}
	return nil
}

// compact rewrites a session's log as the one event that makes it as it is now. l.mu is held.
func (l *Logs) compact(store core.Store, id core.SessionID) error {
	se, ok := store.State().Session(id)
	path := l.eventsPath(id)
	_ = os.Rename(path, path+".broken")
	if !ok {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	rec := core.NewRecorder(f).Keep(l.vault(id))
	rec.Record(core.SessionSynced{Session: se.Snapshot()})
	l.recs[id] = &sessionLog{f: f, rec: rec}
	return rec.Err()
}

// openLog opens a session's log to append to, its last event seq. l.mu is held.
func (l *Logs) openLog(id core.SessionID, seq uint64) error {
	if err := os.MkdirAll(l.env.SessionDir(id), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.eventsPath(id), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	l.recs[id] = &sessionLog{f: f, rec: core.ResumeRecorder(f, seq).Keep(l.vault(id))}
	return nil
}

// vault keeps the text a session's log leaves out: redacted comments' bodies. Nil if its file can't be read.
func (l *Logs) vault(id core.SessionID) *core.Vault {
	v, err := core.OpenVault(filepath.Join(l.env.SessionDir(id), "redacted.json"))
	if err != nil {
		l.env.Log.Warn("session vault", "session", id, "err", err)
		return nil
	}
	return v
}

// Record keeps every session event in its session's log.
func (l *Logs) Record(ev core.Event) {
	id := ev.Scope()
	if id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unlogged[id] {
		return
	}
	r := l.recs[id]
	if r == nil {
		if err := l.openLog(id, 0); err != nil {
			l.failed(id, err)
			return
		}
		r = l.recs[id]
	}
	r.rec.Record(ev)
	if err := r.rec.Err(); err != nil {
		l.failed(id, err)
	}
}

// Logged says whether a session's events are being recorded.
func (l *Logs) Logged(id core.SessionID) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recs[id] != nil && !l.unlogged[id]
}

// failed stops recording a session's events, and says so: logged, and as a notice in the session. l.mu is held.
func (l *Logs) failed(id core.SessionID, err error) {
	l.unlogged[id] = true
	l.env.Log.Error("session log", "session", id, "err", err)
	n := core.Notice{ID: "session.log", Severity: core.SevError, Source: "host", Session: id, Title: "Session not saved",
		Text: "This session's changes are no longer saved to disk, so a restart loses them.", Detail: err.Error(), Time: l.env.Now().UTC()}
	l.env.Go("session log", func() { _ = l.st.Emit(core.Notified{Notice: n}) })
}

// Forget closes a deleted session's logs and removes its directory.
func (l *Logs) Forget(id core.SessionID) error {
	l.mu.Lock()
	if r := l.recs[id]; r != nil {
		_ = r.f.Close()
		delete(l.recs, id)
	}
	delete(l.unlogged, id)
	if o := l.open[id]; o != nil {
		_ = o.Close()
		delete(l.open, id)
	}
	l.mu.Unlock()
	return os.RemoveAll(l.env.SessionDir(id))
}

// Close closes every log.
func (l *Logs) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var err error
	for _, r := range l.recs {
		err = errors.Join(err, r.f.Close())
	}
	for _, o := range l.open {
		err = errors.Join(err, o.Close())
	}
	return err
}

// Notices is each session's log of notices, kept with the session.
func (l *Logs) Notices() logs.SessionLogs { return noticeLogs{l} }

type noticeLogs struct{ l *Logs }

func (s noticeLogs) Open(id core.SessionID) (logs.SessionLog, error) { return noticeLog{s.l, id}, nil }

type noticeLog struct {
	l  *Logs
	id core.SessionID
}

func (n noticeLog) Append(rec logs.Record) (err error) {
	defer n.l.env.Recover("a session log", &err)
	sl, err := n.l.openNotices(n.id)
	if err != nil {
		return err
	}
	return sl.Append(rec)
}

func (n noticeLog) Records() (_ []logs.Record, err error) {
	defer n.l.env.Recover("a session log", &err)
	sl, err := n.l.openNotices(n.id)
	if err != nil {
		return nil, err
	}
	return sl.Records()
}

func (n noticeLog) Close() error { return nil }

// openNotices is a session's notice log, opened once and kept until the host closes.
func (l *Logs) openNotices(id core.SessionID) (logs.SessionLog, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if o := l.open[id]; o != nil {
		return o, nil
	}
	o, err := l.notices.Open(id)
	if err != nil {
		return nil, err
	}
	l.open[id] = o
	return o, nil
}
