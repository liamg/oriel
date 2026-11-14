package shell

import (
	"reflect"
	"slices"
	"sync"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// widgetServices are the services plugins give the UI's widgets, by type; the latest of a type is the one provided.
type widgetServices struct {
	mu sync.Mutex
	by map[reflect.Type][]*widgetService
}

type widgetService struct{ v any }

// apply provides u with the latest service of each type.
func (w *widgetServices) apply(u *ui.UI) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for t, s := range w.by {
		ui.ProvideType(u, t, s[len(s)-1].v)
	}
}

func (w *widgetServices) set(u *ui.UI, t reflect.Type) {
	if u == nil {
		return
	}
	var v any
	if s := w.by[t]; len(s) > 0 {
		v = s[len(s)-1].v
	}
	ui.ProvideType(u, t, v)
}

func (h *pluginHost) ProvideUI(t reflect.Type, v any) registry.Disposable {
	w := &h.sh.widgets
	s := &widgetService{v}
	w.mu.Lock()
	if w.by == nil {
		w.by = map[reflect.Type][]*widgetService{}
	}
	w.by[t] = append(w.by[t], s)
	w.set(h.sh.ui.Load(), t)
	w.mu.Unlock()
	return registry.DisposeFunc(func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.by[t] = slices.DeleteFunc(w.by[t], func(x *widgetService) bool { return x == s })
		if len(w.by[t]) == 0 {
			delete(w.by, t)
		}
		w.set(h.sh.ui.Load(), t)
	})
}
