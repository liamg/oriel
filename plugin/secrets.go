package plugin

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Secrets is where named secrets are kept. Plugins never read
// them: the host sends one only where the plugin's grant binds it.
type Secrets interface {
	Secret(name string) (string, bool)
	SetSecret(name, value string) error // an empty value deletes it
	SecretNames() []string
}

// SecretFile keeps secrets in a JSON file only its owner can read.
type SecretFile struct {
	Path string
	mu   sync.Mutex
}

func (f *SecretFile) load() (map[string]string, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	return m, json.Unmarshal(b, &m)
}

func (f *SecretFile) Secret(name string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return "", false
	}
	v, ok := m[name]
	return v, ok && v != ""
}

func (f *SecretFile) SetSecret(name, value string) error {
	if name == "" {
		return errors.New("a secret needs a name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if value == "" {
		delete(m, name)
	} else {
		m[name] = value
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func (f *SecretFile) SecretNames() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, _ := f.load()
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
