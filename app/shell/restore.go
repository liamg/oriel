package shell

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui/layout"
)

// DirState is a state file for the directory the app is open on, name-<hash of the directory>.json; "" without one.
func (cfg Config) DirState(name string) string { return dirStateFor(cfg.StateDir, cfg.WorkDir, name) }

// dirStateFor is the state file name for dir, under stateDir; "" without either.
func dirStateFor(stateDir, dir, name string) string {
	if stateDir == "" || dir == "" {
		return ""
	}
	sum := 0
	for _, r := range dir {
		sum = sum*31 + int(r)
	}
	return filepath.Join(stateDir, fmt.Sprintf("%s-%x.json", name, uint32(sum)))
}

// savedLayout is the layout as it comes back when the app starts again on the same directory: the
// layout, open buffers and cursor positions, with each view instance's state (uireg.Persisted).
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
	p, can := uireg.As[uireg.Persisted](view)
	if !can {
		return nil, false
	}
	registry.Call(func() error { st, ok = p.Persist(v.Instance); return nil })
	return st, ok
}

// back brings back the layout the window left this directory with, its views as they were, unless something is open
// already; a view whose instance has gone since closes.
func (l *layoutPlugin) back(h plugin.Host) bool {
	p := l.state.Path()
	s, ok := l.live[p]
	if !ok || len(l.root.All()) > 0 {
		return false
	}
	delete(l.live, p)
	if err := l.root.Load(s); err != nil {
		h.Log().Warn("the layout left", "path", p, "err", err)
		return false
	}
	l.kept = l.keptAt[p]
	for _, v := range l.root.All() {
		if v.Instance != "" && !alive(h, v) {
			l.root.Close(v)
		}
	}
	return true
}

// alive says v's view is still there, and still has v's instance if it can say.
func alive(h plugin.Host, v layout.ViewRef) bool {
	view, ok := h.ViewByID(v.ID)
	if !ok {
		return false
	}
	if _, can := uireg.As[uireg.Persisted](view); !can {
		return true
	}
	_, ok = persist(h, v)
	return ok
}

// restore brings back the layout saved for this directory, as its state file had it, unless something is open
// already.
func (l *layoutPlugin) restore(h plugin.Host) {
	b, path := l.saved, l.state.Path()
	if b == nil || len(l.root.All()) > 0 {
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
		view, ok := viewFor(h, v.ID)
		if !ok {
			l.root.Close(v)
			continue
		}
		if v.Instance == "" {
			continue
		}
		old := layout.ViewRef{ID: v.ID, Instance: strings.TrimPrefix(v.Instance, revivedPrefix)}
		p, can := uireg.As[uireg.Persisted](view)
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
	if !l.restored || l.state.Path() == "" {
		return
	}
	b := l.capture(h)
	if b == nil || bytes.Equal(b, l.kept) {
		return
	}
	l.kept = b
	l.state.Write(b)
}

// stateWriter writes a state file off the loop, the latest contents winning over any still waiting.
type stateWriter struct {
	mu      sync.Mutex
	path    string
	pending []byte
	busy    bool
	done    sync.WaitGroup
	track   func(what string) (end func()) // counts a write under way; nil counts nothing
}

func (w *stateWriter) write(path string, b []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.path, w.pending = path, b
	if !w.busy {
		w.busy = true
		w.done.Add(1)
		end := func() {}
		if w.track != nil {
			end = w.track("writing " + filepath.Base(path))
		}
		go w.run(end)
	}
}

func (w *stateWriter) run(end func()) {
	defer w.done.Done()
	defer end()
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
		atomicfile.Write(path, b, 0o600)
	}
}

// flush waits for what's been written to land.
func (w *stateWriter) flush() { w.done.Wait() }
