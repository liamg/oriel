package stats

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/agent/price"
)

// cacheVersion changes with the shape of a scan: a stats file from another is dropped and the logs read again.
const cacheVersion = 4

// Log is a file a session's agent writes, in a format Read knows.
type Log struct {
	Path   string `json:"path"`
	Format string `json:"format"`
}

// Store keeps every session's scans, and keeps them in a stats file, so a restart reads only what's been written
// since rather than every log again. A log read at other prices than the book's is read again, if it's still there.
type Store struct {
	file   string
	prices func() *price.Book

	mu       sync.Mutex
	loaded   bool
	dirty    bool // read since the stats file was last written
	scans    map[string]*Scan // by session, then path: key(session, path)
	snapshot *price.Book
}

// NewStore keeps its scans in file ("" keeps them in memory only), priced with the book prices gives (the
// snapshot's if nil).
func NewStore(file string, prices func() *price.Book) *Store {
	return &Store{file: file, prices: prices, scans: map[string]*Scan{}}
}

// book is the prices to read with.
func (s *Store) book() *price.Book {
	if s.prices != nil {
		if b := s.prices(); b != nil {
			return b
		}
	}
	if s.snapshot == nil {
		s.snapshot = price.Default()
	}
	return s.snapshot
}

func key(session, path string) string { return session + "\x00" + path }

type cacheFile struct {
	Version int              `json:"version"`
	Scans   map[string]*Scan `json:"scans"`
}

// Scan brings session's logs up to date, reading only what each has had appended, and returns their scans. Save
// keeps what it read.
func (s *Store) Scan(session string, logs []Log) []*Scan {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	b := s.book()
	var out []*Scan
	for _, l := range logs {
		k := key(session, l.Path)
		sc := s.scans[k]
		fi, err := statLog(l.Path)
		if sc == nil || err == nil && sc.Priced != b.Version() {
			sc = New(l.Path, l.Format)
			s.scans[k] = sc
		}
		if err == nil && (fi.Size() != sc.Size || sc.Offset == 0) {
			sc.Prices(b)
			s.dirty = sc.Read() || s.dirty
		}
		out = append(out, sc)
	}
	return out
}

// Save writes the stats file, if a Scan read anything since it was last written.
func (s *Store) Save() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dirty {
		s.save()
		s.dirty = false
	}
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
