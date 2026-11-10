package kit

import (
	"os"
	"path/filepath"
	"sync"
)

// Writer writes files off the loop, one at a time: each path's latest content, the folders it's in made first. A
// write asked for while an older one of the same path waits replaces it. Its Busy is what a plugin hands a
// registry.Busy. The zero Writer is ready.
type Writer struct {
	mu      sync.Mutex
	pending map[string]write
	order   []string
	busy    bool
	idle    sync.Cond
	running Busy
	// Failed is told of a write that failed, off the loop; nil drops it.
	Failed func(path string, err error)
}

type write struct {
	data   []byte
	remove bool
}

// Write writes data to path.
func (w *Writer) Write(path string, data []byte) { w.put(path, write{data: data}) }

// Remove removes path.
func (w *Writer) Remove(path string) { w.put(path, write{remove: true}) }

func (w *Writer) put(path string, wr write) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.pending == nil {
		w.pending = map[string]write{}
	}
	if _, ok := w.pending[path]; !ok {
		w.order = append(w.order, path)
	}
	w.pending[path] = wr
	if !w.busy {
		w.busy = true
		w.running.Go(w.drain)
	}
}

func (w *Writer) drain() {
	for {
		w.mu.Lock()
		if len(w.order) == 0 {
			w.busy = false
			w.cond().Broadcast()
			w.mu.Unlock()
			return
		}
		path := w.order[0]
		w.order = w.order[1:]
		wr := w.pending[path]
		delete(w.pending, path)
		w.mu.Unlock()
		var err error
		if wr.remove {
			if err = os.Remove(path); os.IsNotExist(err) {
				err = nil
			}
		} else if err = os.MkdirAll(filepath.Dir(path), 0o755); err == nil {
			err = os.WriteFile(path, wr.data, 0o644)
		}
		if err != nil && w.Failed != nil {
			w.Failed(path, err)
		}
	}
}

func (w *Writer) cond() *sync.Cond {
	if w.idle.L == nil {
		w.idle.L = &w.mu
	}
	return &w.idle
}

// Wait waits until every write asked for is done.
func (w *Writer) Wait() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for w.busy {
		w.cond().Wait()
	}
}

// Busy says a write asked for isn't done.
func (w *Writer) Busy() bool { return w.running.Busy() }
