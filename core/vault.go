package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/internal/atomicfile"
)

// Sealed is an event holding text its log mustn't keep: Seal is the event as the log has it, and that text by key;
// Unseal puts the text back from get.
type Sealed interface {
	Seal() (Event, map[string]string)
	Unseal(get func(key string) (string, bool)) Event
}

// Vault keeps the text sealed events take out of their log, in a file of its own that is never shared.
type Vault struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// OpenVault is the vault in the file at path, empty while there's none.
func OpenVault(path string) (*Vault, error) {
	v := &Vault{path: path, m: map[string]string{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &v.m); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Vault) Get(key string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.m[key]
	return s, ok
}

// Put keeps texts, writing the file when one is new or changed.
func (v *Vault) Put(texts map[string]string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	changed := false
	for k, s := range texts {
		if old, ok := v.m[k]; !ok || old != s {
			v.m[k], changed = s, true
		}
	}
	if !changed {
		return nil
	}
	b, err := json.Marshal(v.m)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	return atomicfile.WriteSecret(v.path, b)
}

// sealThreads are threads with each redacted comment's body taken out into texts.
func sealThreads(session SessionID, threads []Thread, texts map[string]string) []Thread {
	if !slices.ContainsFunc(threads, holdsRedacted) {
		return threads
	}
	out := slices.Clone(threads)
	for i, t := range out {
		if !holdsRedacted(t) {
			continue
		}
		out[i].Comments = slices.Clone(t.Comments)
		for j, c := range out[i].Comments {
			if c.Redacted && c.Body != "" {
				texts[commentKey(session, t.ID, c.ID)] = c.Body
				out[i].Comments[j].Body = ""
			}
		}
	}
	return out
}

func holdsRedacted(t Thread) bool {
	return slices.ContainsFunc(t.Comments, func(c Comment) bool { return c.Redacted && c.Body != "" })
}

// unsealThreads puts back the bodies sealThreads took out.
func unsealThreads(session SessionID, threads []Thread, get func(string) (string, bool)) {
	for i, t := range threads {
		for j, c := range t.Comments {
			if c.Redacted && c.Body == "" {
				if s, ok := get(commentKey(session, t.ID, c.ID)); ok {
					threads[i].Comments[j].Body = s
				}
			}
		}
	}
}

func commentKey(session SessionID, thread ThreadID, comment string) string {
	return string(session) + "/" + string(thread) + "/" + comment
}

func (e ThreadUpdated) Seal() (Event, map[string]string) {
	texts := map[string]string{}
	e.Thread = sealThreads(e.Session, []Thread{e.Thread}, texts)[0]
	return e, texts
}

func (e ThreadUpdated) Unseal(get func(string) (string, bool)) Event {
	ts := []Thread{e.Thread}
	unsealThreads(e.Session, ts, get)
	e.Thread = ts[0]
	return e
}

func (e SessionSynced) Seal() (Event, map[string]string) {
	texts := map[string]string{}
	e.Session.Threads = sealThreads(e.Session.ID, e.Session.Threads, texts)
	return e, texts
}

func (e SessionSynced) Unseal(get func(string) (string, bool)) Event {
	unsealThreads(e.Session.ID, e.Session.Threads, get)
	return e
}
