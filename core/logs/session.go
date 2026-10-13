package logs

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/core"
)

// SessionLogs keeps one log per session, which survives a restart of whatever holds it. Workers will hold
// them (AI-1); until then the app keeps them on disk with Dir.
type SessionLogs interface {
	Open(id core.SessionID) (SessionLog, error)
}

type SessionLog interface {
	Append(r Record) error
	Records() ([]Record, error) // oldest first
	Close() error
}

// Dirs keeps each session's log as JSON lines in <Root>/<id>/session.log.
type Dirs struct {
	Root string
}

func (d Dirs) Open(id core.SessionID) (SessionLog, error) {
	if id == "" || filepath.Base(string(id)) != string(id) || id == "." || id == ".." {
		return nil, fmt.Errorf("bad session id %q", id)
	}
	dir := filepath.Join(d.Root, string(id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "session.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &fileLog{path: path, f: f}, nil
}

type fileLog struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

func (l *fileLog) Append(r Record) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return fs.ErrClosed
	}
	_, err = l.f.Write(append(b, '\n'))
	return err
}

// Records reads the log back. A torn last line, from a crash mid-write, is skipped.
func (l *fileLog) Records() ([]Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.Open(l.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

func (l *fileLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// Router sends each record to exactly one log, by its session: Oriel's, or that session's. Every record also
// goes to the ring, which the log panel reads in every scope.
type Router struct {
	Ring     *Ring
	Oriel    Sink
	Sessions SessionLogs

	mu   sync.Mutex
	open map[core.SessionID]SessionLog
	errs []error
}

func (r *Router) Write(rec Record) {
	if rec.Session == "" {
		if r.Oriel != nil {
			r.Oriel.Write(rec)
		}
	} else if l, err := r.session(rec.Session); err == nil {
		r.note(l.Append(rec))
	} else {
		r.note(err)
	}
	if r.Ring != nil {
		r.Ring.Write(rec)
	}
}

// Load opens a session's log and puts what it already holds into the ring, so the panel shows its history.
func (r *Router) Load(id core.SessionID) error {
	l, err := r.session(id)
	if err != nil {
		return err
	}
	recs, err := l.Records()
	if r.Ring != nil {
		for _, rec := range recs {
			r.Ring.Write(rec)
		}
	}
	return err
}

func (r *Router) session(id core.SessionID) (SessionLog, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l, ok := r.open[id]; ok {
		return l, nil
	}
	if r.Sessions == nil {
		return nil, errors.New("no session logs")
	}
	l, err := r.Sessions.Open(id)
	if err != nil {
		return nil, err
	}
	if r.open == nil {
		r.open = map[core.SessionID]SessionLog{}
	}
	r.open[id] = l
	return l, nil
}

func (r *Router) note(err error) {
	if err == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.errs) < 10 {
		r.errs = append(r.errs, err)
	}
}

// Err is what went wrong writing session logs, if anything.
func (r *Router) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return errors.Join(r.errs...)
}

// Close closes every session log the router opened.
func (r *Router) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var err error
	for id, l := range r.open {
		err = errors.Join(err, l.Close())
		delete(r.open, id)
	}
	return err
}
