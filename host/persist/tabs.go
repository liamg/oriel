package persist

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/atomicfile"
)

// Strip keeps the window's tab strip as it was left (tabs.json): the sessions' logs bring each tab back, but not their
// order across sessions, nor which one was in front.
type Strip struct {
	path string
	mu   sync.Mutex
	last core.TabsRestored
}

// NewStrip keeps the strip in home.
func NewStrip(home string) *Strip { return &Strip{path: filepath.Join(home, "tabs.json")} }

// Restore puts the strip back on store as it was last saved, if it was.
func (s *Strip) Restore(store core.Store) error {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var t core.TabsRestored
	if err := json.Unmarshal(b, &t); err != nil {
		return nil // a strip that can't be read is the logs' order
	}
	return store.Apply(t)
}

// Track saves the strip whenever an event changes it.
func (s *Strip) Track(store core.Store) {
	store.Subscribe(core.Filter{}, func(ev core.Event) {
		switch ev.(type) {
		case core.TabOpened, core.TabClosed, core.TabMoved, core.TabsRestored, core.SessionFocused, core.SessionClosed,
			core.SessionArchived:
			s.save(store.State())
		}
	})
}

func (s *Strip) save(st core.State) {
	t := core.TabsRestored{Tabs: slices.Clone(st.Tabs), Focus: st.Focus}
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Equal(t.Tabs, s.last.Tabs) && t.Focus == s.last.Focus {
		return
	}
	b, err := json.Marshal(t)
	if err != nil {
		return
	}
	if atomicfile.Write(s.path, b, 0o600) == nil {
		s.last = t
	}
}
