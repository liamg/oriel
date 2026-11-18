package persist

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/liamg/oriel/internal/atomicfile"
)

// fileVault is a session's core.Vault: the text sealed events take out of its log, in a file of its own that is
// never shared.
type fileVault struct {
	path string
	mu   sync.Mutex
	m    map[string]string
}

// openVault is the vault in the file at path, empty while there's none.
func openVault(path string) (*fileVault, error) {
	v := &fileVault{path: path, m: map[string]string{}}
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

func (v *fileVault) Get(key string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.m[key]
	return s, ok
}

// Put keeps texts, writing the file when one is new or changed.
func (v *fileVault) Put(texts map[string]string) error {
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
