package shell

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/core"
)

// keptStore is Host.Keep's: what plugins keep per session, held by the window across reopens and saved in the
// state directory across restarts.
type keptStore struct {
	mu     sync.Mutex
	path   string // "" keeps it in memory only
	by     map[core.SessionID]map[string]json.RawMessage
	writer stateWriter
}

func newKeptStore(stateDir string, log *slog.Logger) *keptStore {
	k := &keptStore{by: map[core.SessionID]map[string]json.RawMessage{}}
	if stateDir == "" {
		return k
	}
	k.path = filepath.Join(stateDir, "kept.json")
	b, err := os.ReadFile(k.path)
	if err == nil {
		if err := json.Unmarshal(b, &k.by); err != nil {
			log.Warn("the kept state", "path", k.path, "err", err)
			k.by = map[core.SessionID]map[string]json.RawMessage{}
		}
	}
	return k
}

func (k *keptStore) get(id core.SessionID, key string) (json.RawMessage, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.by[id][key]
	return v, ok
}

// set keeps v under id's key, or forgets it when v is nil.
func (k *keptStore) set(id core.SessionID, key string, v any) error {
	var b json.RawMessage
	if v != nil {
		var err error
		if b, err = json.Marshal(v); err != nil {
			return err
		}
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if b == nil {
		delete(k.by[id], key)
		if len(k.by[id]) == 0 {
			delete(k.by, id)
		}
	} else {
		if k.by[id] == nil {
			k.by[id] = map[string]json.RawMessage{}
		}
		k.by[id][key] = b
	}
	k.save()
	return nil
}

// forget drops everything kept for a session that's gone.
func (k *keptStore) forget(id core.SessionID) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.by[id]; ok {
		delete(k.by, id)
		k.save()
	}
}

// save writes the store off the loop; k.mu is held.
func (k *keptStore) save() {
	if k.path == "" {
		return
	}
	b, err := json.Marshal(k.by)
	if err == nil {
		k.writer.write(k.path, b)
	}
}

func (k *keptStore) flush() { k.writer.flush() }
