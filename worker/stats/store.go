package stats

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

// cacheVersion changes with the shape of a scan: a stats file from another is dropped and the logs read again.
const cacheVersion = 3

// Log is a file a session's agent writes, in a format Read knows.
type Log struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// Store keeps every session's scans, and keeps them in a stats file, so a restart reads only what's been written
// since rather than every log again.
type Store struct {
	file string

	mu     sync.Mutex
	loaded bool
	scans  map[string]*Scan // by session, then path: key(session, path)
}

// NewStore keeps its scans in file; "" keeps them in memory only.
func NewStore(file string) *Store { return &Store{file: file, scans: map[string]*Scan{}} }

func key(session, path string) string { return session + "\x00" + path }

type cacheFile struct {
	Version int              `json:"version"`
	Scans   map[string]*Scan `json:"scans"`
}

// Scan brings session's logs up to date, reading only what each has had appended, and returns their scans.
func (s *Store) Scan(session string, logs []Log) []*Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	var out []*Scan
	changed := false
	for _, l := range logs {
		k := key(session, l.Path)
		sc := s.scans[k]
		if sc == nil {
			sc = New(l.Path, l.Format)
			s.scans[k] = sc
		}
		if fi, err := os.Stat(l.Path); err == nil && fi.Size() != sc.Size || sc.Offset == 0 {
			changed = sc.Read() || changed
		}
		out = append(out, sc)
	}
	if changed {
		s.save()
	}
	return out
}

// Scans are what was last read of session's logs, without reading them.
func (s *Store) Scans(session string) []*Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	var out []*Scan
	for k, sc := range s.scans {
		if len(k) > len(session) && k[:len(session)+1] == session+"\x00" {
			out = append(out, sc)
		}
	}
	return out
}

func (s *Store) load() {
	if s.loaded {
		return
	}
	s.loaded = true
	if s.file == "" {
		return
	}
	b, err := os.ReadFile(s.file)
	if err != nil {
		return
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil || c.Version != cacheVersion {
		return
	}
	for k, sc := range c.Scans {
		if sc != nil {
			sc.ensure()
			s.scans[k] = sc
		}
	}
}

// save writes the stats file, atomically.
func (s *Store) save() {
	if s.file == "" {
		return
	}
	c := cacheFile{Version: cacheVersion, Scans: maps.Clone(s.scans)}
	b, err := json.Marshal(c)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(s.file), 0o755) != nil {
		return
	}
	tmp := s.file + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, s.file)
	}
}
