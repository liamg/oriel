package shell

import (
	"cmp"
	"context"
	"encoding/json"
	"image"
	"log/slog"
	"slices"
	"sync"

	"github.com/liamg/oriel/app/kit"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

type pluginHost struct {
	uireg.Registry
	store  core.Store
	source string
	logs   *Logs
	log    *slog.Logger
	sh     *shell
}

// host is what a plugin registered as source sees.
func (sh *shell) host(source string) *pluginHost {
	return &pluginHost{Registry: uireg.WithSource(sh.cfg.Registry, source), store: sh.store, source: source, logs: sh.cfg.Logs,
		log: sh.cfg.Logs.Logger(source), sh: sh}
}

func (h *pluginHost) Layout() *layout.Root { return h.sh.layout.root }

func (h *pluginHost) Subscribe(f core.Filter, fn func(core.Event)) func() {
	return h.store.Subscribe(f, fn)
}
func (h *pluginHost) Redraw() { h.sh.poke() }

func (h *pluginHost) DecorationsChanged(id string) {
	h.Registry.DecorationsChanged(id)
	h.sh.poke()
}

func (h *pluginHost) Do(fn func()) {
	h.sh.todoMu.Lock()
	h.sh.todo = append(h.sh.todo, fn)
	h.sh.todoMu.Unlock()
	h.sh.poke()
}

// runTodo runs what Host.Do queued, on the loop.
func (sh *shell) runTodo() {
	sh.todoMu.Lock()
	fns := sh.todo
	sh.todo = nil
	sh.todoMu.Unlock()
	for _, fn := range fns {
		fn()
	}
}

func (h *pluginHost) UI() plugin.UIView {
	if u := h.sh.ui.Load(); u != nil {
		return u
	}
	return nil
}

// noSession is the session runtime options go to while none is in front.
const noSession = "oriel"

func (h *pluginHost) Here() registry.At {
	at := registry.At{Session: string(h.store.Focus()), Project: h.sh.workDir()}
	if at.Session == "" {
		at.Session = noSession
	}
	if ed, ok := registry.EditorsOf(h); ok {
		at = ed.At(at)
	}
	return at
}

func (h *pluginHost) CurrentTheme() *theme.Resolved {
	if h.sh.theme == nil {
		return nil
	}
	return h.sh.theme()
}

func (h *pluginHost) Load(p plugin.Plugin) (registry.Disposable, error) {
	if err := p.Activate(context.Background(), h.sh.host(p.Manifest().ID)); err != nil {
		return nil, err
	}
	h.sh.loaded = append(h.sh.loaded, p)
	return kit.OnDispose(func() {
		h.sh.loaded = slices.DeleteFunc(h.sh.loaded, func(q plugin.Plugin) bool { return q == p })
		if err := p.Deactivate(context.Background()); err != nil {
			h.log.Warn("stopping a plugin", "plugin", p.Manifest().ID, "err", err)
		}
	}), nil
}

func (h *pluginHost) Plugins() []plugin.Manifest {
	var out []plugin.Manifest
	if h.sh.active != nil {
		for _, p := range *h.sh.active {
			if !waits(p) {
				out = append(out, p.Manifest())
			}
		}
	}
	for _, p := range h.sh.loaded {
		out = append(out, p.Manifest())
	}
	return out
}

func (h *pluginHost) Shot(th *theme.Resolved) *image.RGBA {
	if h.sh.shot == nil {
		return nil
	}
	return h.sh.shot(th)
}

func (h *pluginHost) Pending() plugin.Pending {
	if k := h.sh.keys; k != nil && len(k.Pending()) > 0 {
		p := plugin.Pending{Keys: k.Pending(), Modes: k.Modes(), Since: k.Since()}
		if m := k.Complete(); m != nil && acceptKey(h.sh.cfg.Registry, p.Modes, p.Keys) {
			p.Runs = cmp.Or(m.Desc, m.Command)
		}
		if ed, ok := registry.EditorsOf(h); ok {
			p.Hints = ed.Hints(p.Keys) // keys no mapping takes go on to the engine
		}
		return p
	}
	if ed, ok := registry.EditorsOf(h); ok {
		return ed.Pending()
	}
	return plugin.Pending{}
}

func (h *pluginHost) HoldPending() {
	if k := h.sh.keys; k != nil {
		k.Hold()
	}
}

func (h *pluginHost) PaneRect(v layout.ViewRef) (render.Rect, bool) {
	w := h.sh.layout.root.Find(v)
	if w == nil {
		return render.Rect{}, false
	}
	r, ok := h.sh.layout.rects[w.Leaf]
	return r, ok
}

func (h *pluginHost) PaneTab(id ui.ID) (layout.ViewRef, bool) {
	u := h.sh.ui.Load()
	if u == nil {
		return layout.ViewRef{}, false
	}
	return h.sh.layout.panes.TabAt(u, id)
}

func (h *pluginHost) Session(id core.SessionID) (core.Session, bool) { return h.store.Session(id) }
func (h *pluginHost) Sessions() []core.Session                       { return h.store.Sessions() }
func (h *pluginHost) Log() *slog.Logger                              { return h.log }
func (h *pluginHost) State() core.State                              { return h.store.State() }
func (h *pluginHost) Apply(ev core.Event) error                      { return h.store.Apply(ev) }
func (h *pluginHost) WindowFocus() (bool, <-chan struct{})           { return h.sh.focus.now() }
func (h *pluginHost) Framed(fn func()) func()                        { return h.sh.framed.On(fn) }

func (h *pluginHost) KeyModes() []string {
	if h.sh.modes == nil {
		return nil
	}
	return h.sh.modes()
}

func (h *pluginHost) Kept(session core.SessionID, key string) (json.RawMessage, bool) {
	return h.sh.kept.get(session, key)
}

func (h *pluginHost) Keep(session core.SessionID, key string, v any) error {
	return h.sh.kept.set(session, key, v)
}

func (h *pluginHost) Claim(key string) (func(), bool) {
	if _, held := h.sh.claims.LoadOrStore(key, struct{}{}); held {
		return func() {}, false
	}
	return sync.OnceFunc(func() { h.sh.claims.Delete(key) }), true
}

// WindowRegistry is the window's registry, which the host is a view of.
func (h *pluginHost) WindowRegistry() registry.Registry {
	if h.sh == nil {
		return nil
	}
	return h.sh.cfg.Registry
}

func (h *pluginHost) Notify(n core.Notice) {
	if n.Source == "" {
		n.Source = h.source
	}
	if err := h.store.Apply(core.Notified{Notice: redactNotice(n, h.logs.Redact)}); err != nil {
		h.log.Error("notify", "id", n.ID, "err", err)
	}
}

func (h *pluginHost) Dismiss(id string) {
	for _, n := range h.store.State().Notifications {
		if n.ID == id && !n.Dismissed {
			h.store.Apply(core.Dismissal(n))
		}
	}
}
