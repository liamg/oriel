package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/layout"
)

// dirState is a state file for the directory the app is open on, name-<hash of the directory>.json; "" without one.
func (sh *shell) dirState(name string) string {
	if sh.cfg.StateDir == "" || sh.cfg.WorkDir == "" {
		return ""
	}
	sum := 0
	for _, r := range sh.cfg.WorkDir {
		sum = sum*31 + int(r)
	}
	return filepath.Join(sh.cfg.StateDir, fmt.Sprintf("%s-%x.json", name, uint32(sum)))
}

// savedLayout is the layout as it comes back when the app starts again on the same directory (sessions.md: "the
// layout, open buffers and cursor positions come back"), with each view instance's state (registry.Persisted).
type savedLayout struct {
	Layout json.RawMessage            `json:"layout"`
	Views  map[string]json.RawMessage `json:"views,omitempty"` // by the instance's reference
}

// revivedPrefix marks an instance from the saved layout until its view revives it, so it can't meet a new one.
const revivedPrefix = "saved:"

// capture is the layout as it would come back: an instance whose view can't keep it is left out.
func (l *layoutPlugin) capture(h plugin.Host) []byte {
	r, err := layout.Restore(l.root.Save())
	if err != nil {
		return nil
	}
	views := map[string]json.RawMessage{}
	for _, v := range r.All() {
		if v.Instance == "" {
			continue
		}
		if st, ok := persist(h, v); ok {
			views[v.String()] = st
			continue
		}
		r.Close(v)
	}
	b, _ := json.Marshal(savedLayout{Layout: json.RawMessage(r.Save()), Views: views})
	return b
}

func persist(h plugin.Host, v layout.ViewRef) (st json.RawMessage, ok bool) {
	view, found := h.ViewByID(v.ID)
	if !found {
		return nil, false
	}
	p, can := registry.As[registry.Persisted](view)
	if !can {
		return nil, false
	}
	registry.Call(func() error { st, ok = p.Persist(v.Instance); return nil })
	return st, ok
}

// restore brings back the layout saved for this directory, unless something is open already.
func (l *layoutPlugin) restore(h plugin.Host) {
	path := l.sh.dirState("layout")
	b, err := os.ReadFile(path)
	if path == "" || err != nil || len(l.root.All()) > 0 {
		return
	}
	var s savedLayout
	if err := json.Unmarshal(b, &s); err != nil {
		h.Log().Warn("the saved layout", "path", path, "err", err)
		return
	}
	if err := l.root.Load(layout.Snapshot(s.Layout)); err != nil {
		h.Log().Warn("the saved layout", "path", path, "err", err)
		return
	}
	for _, v := range l.root.All() {
		if v.Instance != "" {
			l.root.Rename(v, layout.ViewRef{ID: v.ID, Instance: revivedPrefix + v.Instance, Title: v.Title})
		}
	}
	for _, v := range l.root.All() {
		view, ok := h.ViewByID(v.ID)
		if !ok {
			l.root.Close(v)
			continue
		}
		if v.Instance == "" {
			continue
		}
		old := layout.ViewRef{ID: v.ID, Instance: strings.TrimPrefix(v.Instance, revivedPrefix)}
		p, can := registry.As[registry.Persisted](view)
		st, kept := s.Views[old.String()]
		var inst string
		err := fmt.Errorf("%s keeps no state", v.ID)
		if can && kept {
			err = registry.Call(func() (e error) { inst, e = p.Revive(st); return e })
		}
		if err != nil {
			h.Log().Info("a view didn't come back", "view", old.String(), "err", err)
			l.root.Close(v)
			continue
		}
		l.root.Rename(v, layout.ViewRef{ID: v.ID, Instance: inst, Title: v.Title})
	}
}

// keep saves the layout when it has changed since it was last saved.
func (l *layoutPlugin) keep(h plugin.Host) {
	if !l.restored {
		return
	}
	path := l.sh.dirState("layout")
	if path == "" {
		return
	}
	b := l.capture(h)
	if b == nil || bytes.Equal(b, l.kept) {
		return
	}
	l.kept = b
	l.writer.write(path, b)
}

func (l *layoutPlugin) Deactivate(ctx context.Context) error {
	l.writer.flush()
	return l.disposer.Deactivate(ctx)
}

// stateWriter writes a state file off the loop, the latest contents winning over any still waiting.
type stateWriter struct {
	mu      sync.Mutex
	path    string
	pending []byte
	busy    bool
	done    sync.WaitGroup
}

func (w *stateWriter) write(path string, b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.path, w.pending = path, b
	if !w.busy {
		w.busy = true
		w.done.Add(1)
		go w.run()
	}
}

func (w *stateWriter) run() {
	defer w.done.Done()
	for {
		w.mu.Lock()
		path, b := w.path, w.pending
		w.pending = nil
		if b == nil {
			w.busy = false
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
		os.MkdirAll(filepath.Dir(path), 0o755)
		tmp := path + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			os.Rename(tmp, path)
		}
	}
}

// flush waits for what's been written to land.
func (w *stateWriter) flush() { w.done.Wait() }
