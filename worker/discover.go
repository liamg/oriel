package worker

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/worker/stats"
)

// The stats read every log a provider keeps on this machine (agent.Discovered), not only Oriel's sessions': one that
// no Oriel session owns, by its path or the provider's id for it, is a session outside Oriel.

// owned are the logs Oriel's sessions read, by path and by provider and session id.
type owned map[string]bool

func ownKey(provider, session string) string { return provider + "\x00" + session }

func (o owned) has(provider string, t agent.Transcript) bool {
	if o[ownKey(provider, t.Session)] {
		return true
	}
	return slices.ContainsFunc(t.Logs, func(l agent.Log) bool { return o[filepath.Clean(l.Path)] })
}

// outsideID is an outside session's ID: its provider's, then the provider's own.
func outsideID(provider, session string) string { return provider + ":" + session }

// outsideSessions are the logs the providers keep that no Oriel session owns, read up to now: only's alone when it's
// set, else each changed since since.
func (w *Worker) outsideSessions(only core.SessionID, since time.Time, own owned) []stats.Session {
	var out []stats.Session
	for _, p := range w.opts.Providers.List() {
		d, ok := p.(agent.Discovered)
		if !ok {
			continue
		}
		for _, t := range d.Discover() {
			id := outsideID(p.ID(), t.Session)
			if len(t.Logs) == 0 || only != "" && id != string(only) || own.has(p.ID(), t) || only == "" && t.Modified.Before(since) {
				continue
			}
			var logs []stats.Log
			for _, l := range t.Logs {
				logs = append(logs, stats.Log{Path: l.Path, Format: l.Format})
			}
			s := stats.Session{ID: id, Provider: p.ID(), Worker: string(w.opts.ID), Origin: stats.OriginOutside,
				Scans: w.statsStore().Scan(id, logs)}
			main := s.Scans[0]
			s.Name = main.Title
			if main.Cwd != "" {
				root := repoRoot(main.Cwd)
				s.Repos = []stats.Repo{{Name: filepath.Base(root), Dir: root}}
				s.Name = cmp.Or(s.Name, filepath.Base(main.Cwd))
			}
			s.Name = cmp.Or(s.Name, strings.TrimSuffix(filepath.Base(main.Path), ".jsonl"))
			if acct, name := w.accountOf(p.ID()); acct != "" {
				s.Accounts = []stats.Account{{ID: acct, Name: name}}
			}
			out = append(out, s)
		}
	}
	return out
}

// repoRoot is the top of the work tree holding dir, or dir when there's none.
func repoRoot(dir string) string {
	for d := filepath.Clean(dir); ; d = filepath.Dir(d) {
		if gitx.IsRepo(d) {
			return d
		}
		if filepath.Dir(d) == d {
			return filepath.Clean(dir)
		}
	}
}
