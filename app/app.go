package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/liamg/oriel/render/raster"
	"image"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/catalog"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/search"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
)

// The views the window is made of. Each is an ordinary view: override one to replace that part of the screen.
const (
	RootView          = "app.root"          // fills the window
	StatusLineView    = "app.statusline"    // along the bottom
	NotificationsView = "app.notifications" // toasts, floating over everything
	ModalView         = "app.modal"         // when Oriel can't go on without an answer
	OverlayPrefix     = "app.overlay."      // views named so float over the window: the omnibox, which-key
)

// ViewKeyMode is the keymap mode of a view in the layout that has focus but no keys of its own: q closes it.
// EmptyKeyMode is the mode while the main area is empty and shows the start view or the project overview.
const (
	ViewKeyMode  = "view"
	EmptyKeyMode = "empty"
)

type Config struct {
	Store      core.Store        // the materialised state, shared with the session host; Host's, else in-memory, if nil
	Registry   registry.Registry // a fresh one if nil
	Plugins    []plugin.Plugin   // activated in order, after the built-ins
	Logs       *Logs             // where log records go; a ring in memory if nil
	ConfigDir  string            // ~/.config/oriel unless set; user themes live in its themes/
	CrashDir   string            // crash reports are written to and offered from here; none if ""
	Version    string
	Desktop    Desktop  // the clipboard and the browser; the window's, or the OS's, if nil
	Debug      bool     // oriel --debug: the frame-time overlay
	CPUProfile string   // oriel --cpuprofile: a CPU profile written here while the window is open
	Trace      string   // oriel --trace: the execution trace of the seconds before a stall, written here
	Files      []string // open in the editor at start
	WorkDir    string   // the directory open in the window, where relative paths are; none if "" (the start view shows)
	StateDir   string   // state that isn't config, such as the recent directories; logs.StateDir() if ""
	CacheDir   string   // compiled grammars and the like; the user's cache directory's oriel/ if ""
	DataDir    string   // installed plugins, in its plugins/; DefaultDataDir() if ""
	// Host is the session host, in this process; no sessions if nil.
	Host *host.Host
	// Updates is Oriel updating itself (distribution.md); nil in a build that doesn't.
	Updates *Updates
	// Started is called once the window is open and every plugin active: what the updater's health check waits for.
	Started func()
	Voice   VoiceConfig // dictation's microphone, transcriber and speaker; the real ones, from the voice.* options, if nil
	// Net is what plugins' HTTP goes through (http.request); a client of its own if nil.
	Net *http.Client
	// GitHub is how plugins and the plugin registries are read; GitHub's API with your gh login's token if nil.
	GitHub *catalog.GitHub
	// Secrets are the named secrets plugins' requests carry (security.md); secrets.json in StateDir if nil.
	Secrets plugin.Secrets
	// Bundled are the plugins shipped in the binary, loaded by the plugin manager unless turned off or installed.
	Bundled []plugin.Bundled
	// Self is the command that runs oriel itself, for bundled plugins' processes; os.Executable() if nil.
	Self []string
}

var textStyle = ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 13}}

// DefaultConfigDir is $XDG_CONFIG_HOME/oriel, or ~/.config/oriel, on every platform (plugins.md, "Runtime path").
func DefaultConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "oriel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "oriel")
}

// DefaultDataDir is $XDG_DATA_HOME/oriel, or ~/.local/share/oriel, on every platform (catalog.md, Files on disk).
func DefaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "oriel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "oriel")
}

// Run opens a window and runs the app until the window closes or ctx is done.
func Run(ctx context.Context, be render.Backend, cfg Config) (err error) {
	cfg.defaults()
	go search.Ripgrep() // probed now, off the loop, so the search panel finds the answer waiting
	if cfg.CacheDir == "" {
		if d, err := os.UserCacheDir(); err == nil {
			cfg.CacheDir = filepath.Join(d, "oriel")
		}
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = DefaultConfigDir()
	}
	log := cfg.Logs.Logger("core")
	dirty := make(chan struct{}, 1)
	poke := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	if cfg.StateDir == "" {
		cfg.StateDir = logs.StateDir()
	}
	sh, unsubscribe := newShell(cfg, poke)
	defer unsubscribe()
	sh.recents = &recents{path: filepath.Join(cfg.StateDir, "recent.json")}
	if cfg.WorkDir != "" {
		sh.recents.add(cfg.WorkDir, time.Now())
	}
	defer func() {
		if v := recover(); v != nil {
			err = sh.crashed(v, debug.Stack())
		}
	}()
	var active []plugin.Plugin
	deactivate := func() (err error) {
		for _, e := range slices.Backward(active) {
			err = errors.Join(err, e.Deactivate(context.WithoutCancel(ctx)))
		}
		active = nil
		return err
	}
	defer func() {
		err = errors.Join(err, deactivate())
		forgetLanguages(cfg.Registry)
	}()
	activate := func() error {
		_, err := sh.activate(ctx, &active)
		return err
	}
	if err := activate(); err != nil {
		return err
	}
	if x := sh.sessions; x != nil && cfg.Host != nil && cfg.WorkDir != "" && !x.holds(cfg.WorkDir) {
		if err := x.openDirect(ctx, cfg.WorkDir); err != nil {
			log.Error("direct session", "dir", cfg.WorkDir, "err", err)
		}
	}

	place := windowPlace{path: filepath.Join(cfg.StateDir, "window.json")}
	first, _ := chosenTheme(cfg.Registry, false)
	w, err := be.NewWindow(render.WindowOpts{Title: "Oriel", Width: 1280, Height: 800, Frame: place.load(), Background: first.C(theme.BgBase)})
	if err != nil {
		return err
	}
	defer w.Close()
	defer func() {
		if err := place.save(w); err != nil {
			log.Error("window place", "err", err)
		}
	}()
	sh.closeWin = w.Close
	if ch, ok := w.(render.CloseHolder); ok && cfg.Host != nil {
		ch.HoldClose(true) // quitting hands the agents over first
	}
	sh.desktop = cfg.Desktop
	if sh.desktop == nil {
		sh.desktop = windowDesktop(w)
	}

	cancel := cfg.Store.Subscribe(core.Filter{}, func(core.Event) { poke() })
	defer cancel()
	cfg.Logs.Ring.OnWrite(poke)

	l := &loop{sh: sh, debug: cfg.Debug, reg: cfg.Registry, store: cfg.Store, keys: registry.NewResolver(cfg.Registry), win: w, ui: ui.New(be.Shaper(), textStyle), log: log}
	l.stalls = startStallTrace(cfg.Trace, log)
	defer l.stalls.stop()
	l.ui.SetClipboard(sh.desktop)
	l.ui.Title = "Oriel"
	sh.keys, sh.modes = l.keys, l.modes
	sh.ui.Store(l.ui)
	l.keys.Raw = func() bool { return sh.enginePending != nil && len(sh.enginePending().Keys) > 0 }
	sh.theme, sh.shot = func() *theme.Resolved { return l.theme }, l.shot
	l.themeDirty.Store(true)
	retheme := func() { l.themeDirty.Store(true); poke() }
	l.retheme = retheme
	l.themeGen = -1
	defer l.unwatchThemes()
	for _, opt := range []string{"ui.theme", "ui.variant"} {
		defer cfg.Registry.Options().Watch(opt, retheme)()
	}
	if cfg.Started != nil {
		sh.todoMu.Lock()
		sh.todo = append(sh.todo, cfg.Started)
		sh.todoMu.Unlock()
	}
	defer sh.relaunchAfter(&err)
	timer := time.NewTimer(0)
	<-timer.C
	var acts <-chan render.AccessAction
	accessible, _ := w.(render.Accessible)
	if accessible != nil {
		acts = accessible.AccessActions()
	}
	var tree *render.AccessNode
	var held render.Event // read past a run of pointer moves; handled on the next turn
	var drawn, due time.Time
	quiet := false // the last turn changed nothing a frame shows
	// pointed paces pointer moves' frames to one a frameInterval, and builds none for a move that changes nothing.
	pointed := func(ev render.Event, changed bool) {
		if m, ok := ev.(render.PointerEvent); !ok || m.Kind != render.PointerMove {
			return
		}
		if changed && due.IsZero() {
			due = drawn.Add(frameInterval)
		}
		now, next := time.Now(), l.wake(drawn)
		quiet = (due.IsZero() || now.Before(due)) && (next.IsZero() || now.Before(next))
	}
	for {
		l.timed(sh.runTodo)
		framed := !quiet
		quiet = false
		if framed {
			sh.seeNav(l.reg, l.ui.Now())
			var derr error
			l.timed(func() { derr = l.draw() })
			if derr != nil {
				return derr
			}
			drawn, due = time.Now(), time.Time{}
			if l.input.built() {
				l.stalls.stalled()
			}
		}
		// Keys typed behind ones the editor has now taken: looked up, or passed on raw while it's mid-command.
		if acts := l.keys.Pump(l.sh.keyModes(), time.Now()); len(acts) > 0 || l.keys.Queued() {
			l.run(ctx, acts)
			poke()
		}
		if accessible != nil && framed {
			next := l.ui.AccessTree()
			if up := render.AccessDiff(tree, next); len(up.Changed) > 0 || len(up.Removed) > 0 || tree == nil {
				accessible.UpdateAccess(up)
			}
			tree = next
		}
		if d := l.wake(drawn); !d.IsZero() || !due.IsZero() {
			if d.IsZero() || (!due.IsZero() && due.Before(d)) {
				d = due
			}
			timer.Reset(time.Until(d))
		} else {
			timer.Stop()
		}
		if held != nil {
			ev := held
			held = nil
			var changed bool
			l.timed(func() { changed = l.handle(ctx, ev) })
			pointed(ev, changed)
			continue
		}
		select {
		case a := <-acts:
			l.ui.Act(ui.ID(a.ID), a.Action)
		case <-ctx.Done():
			return ctx.Err()
		case <-dirty:
		case to := <-sh.reopen:
			// Open Directory, or another session in front: every plugin starts again on its directory.
			if err := deactivate(); err != nil {
				log.Error("reopen", "dir", to.dir, "err", err)
			}
			sh.cfg.WorkDir, sh.cfg.Files = to.dir, nil
			if to.session == "" && to.dir != "" {
				sh.recents.add(to.dir, time.Now())
			}
			if err := activate(); err != nil {
				return err
			}
			l.themeDirty.Store(true)
		case now := <-timer.C:
			l.run(ctx, l.keys.Timeout(now))
			// and a redraw, for a spring or a tooltip that was due
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			ev, held = lastMove(ev, w.Events())
			var changed bool
			l.timed(func() { changed = l.handle(ctx, ev) })
			pointed(ev, changed)
		}
	}
}

// defaults fills in what Config says it makes when it's nil.
func (cfg *Config) defaults() {
	if cfg.Store == nil && cfg.Host != nil {
		cfg.Store = cfg.Host.Store()
	}
	if cfg.Store == nil {
		cfg.Store = eventstore.New()
	}
	if cfg.Registry == nil {
		cfg.Registry = registry.New()
	}
	if cfg.Logs == nil {
		cfg.Logs = NewLogs(LogOpts{})
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
}

// newShell is the shell over cfg, keeping the store's recent events and logging its notices until unsubscribed.
func newShell(cfg Config, poke func()) (*shell, func()) {
	events := newEventTail(500)
	log := cfg.Logs.Logger("core")
	tail := cfg.Store.Subscribe(core.Filter{}, events.add)
	notices := cfg.Store.Subscribe(core.Filter{}, func(ev core.Event) { logNotice(cfg.Store, log, ev) })
	sh := &shell{cfg: cfg, events: events, poke: poke, reopen: make(chan reopenTo, 1)}
	iconReg.Store(&cfg.Registry)
	return sh, func() { tail(); notices() }
}

// activate starts the built-ins, then cfg.Plugins, each on a host of its own, adding each to active as it starts.
// It answers the last built-in's host.
func (sh *shell) activate(ctx context.Context, active *[]plugin.Plugin) (*pluginHost, error) {
	sh.active = active
	var last *pluginHost
	for _, e := range builtins(sh) {
		last = sh.host("builtin:" + e.Manifest().ID)
		if err := e.Activate(ctx, last); err != nil {
			return nil, fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
		*active = append(*active, e)
	}
	for _, e := range sh.cfg.Plugins {
		if err := e.Activate(ctx, sh.host(e.Manifest().ID)); err != nil {
			return nil, fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
		*active = append(*active, e)
	}
	return last, nil
}

// lastMove is the last of a run of pointer moves already waiting from ev, so a frame isn't built for each, and the
// event read after the run, if any.
func lastMove(ev render.Event, events <-chan render.Event) (render.Event, render.Event) {
	m, ok := ev.(render.PointerEvent)
	if !ok || m.Kind != render.PointerMove {
		return ev, nil
	}
	for {
		select {
		case next, ok := <-events:
			if !ok {
				return m, nil
			}
			if p, ok := next.(render.PointerEvent); ok && p.Kind == render.PointerMove && p.Buttons == m.Buttons && p.Mods == m.Mods {
				m = p
				continue
			}
			return m, next
		default:
			return m, nil
		}
	}
}

type pluginHost struct {
	registry.Registry
	store  core.Store
	source string
	logs   *Logs
	log    *slog.Logger
	sh     *shell
}

// host is what a plugin registered as source sees.
func (sh *shell) host(source string) *pluginHost {
	return &pluginHost{Registry: registry.WithSource(sh.cfg.Registry, source), store: sh.cfg.Store, source: source, logs: sh.cfg.Logs,
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
	at := registry.At{Session: string(h.store.Focus()), Project: h.sh.cfg.WorkDir}
	if at.Session == "" {
		at.Session = noSession
	}
	if h.sh.editorAt != nil {
		at = h.sh.editorAt(at)
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
	return disposeFn(func() {
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
			out = append(out, p.Manifest())
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
			p.Runs = cmpOr(m.Desc, m.Command)
		}
		if h.sh.engineHints != nil {
			p.Hints = h.sh.engineHints(p.Keys) // keys no mapping takes go on to the engine
		}
		return p
	}
	if h.sh.enginePending != nil {
		return h.sh.enginePending()
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

type loop struct {
	themeDirty atomic.Bool
	theme      *theme.Resolved
	osLight    bool // the OS prefers light, as the window last said
	themeGen   int  // the registry's themes, as last watched
	unwatch    []func()
	retheme    func()

	sh        *shell
	debug     bool
	frameTime time.Duration // the last frame's build and record, for the debug overlay

	reg   registry.Registry
	store core.Store
	keys  *registry.Resolver
	win   render.Window
	ui    *ui.UI
	log   *slog.Logger

	input  inputLag
	stalls *stallTrace
}

// lagWarn is how far behind the pointer may fall, from when it moved to a frame built with it, before it's a stall.
const lagWarn = 100 * time.Millisecond

// inputLag times the pointer's moves through the loop, from when each happened to a frame built with it.
type inputLag struct {
	oldest time.Time     // the earliest move not yet in a frame
	drawn  time.Duration // the last frame's: from its oldest move to built, for the debug overlay
}

// arrived notes a move that happened at at, reaching the loop now; one that changed nothing needs no frame.
func (in *inputLag) arrived(at time.Time, changed bool) {
	if at.IsZero() || !changed {
		return
	}
	if in.oldest.IsZero() || at.Before(in.oldest) {
		in.oldest = at
	}
}

// built ends a frame. It reports whether the moves in it had fallen behind.
func (in *inputLag) built() bool {
	if in.oldest.IsZero() {
		return false
	}
	in.drawn = time.Since(in.oldest)
	in.oldest = time.Time{}
	return in.drawn > lagWarn
}

// slowLoop is how long one turn of the loop may take before it's a stall: keys typed meanwhile wait behind it.
const slowLoop = 50 * time.Millisecond

// timed runs fn, and traces a stall when it holds the loop up past slowLoop.
func (l *loop) timed(fn func()) {
	start := time.Now()
	fn()
	if time.Since(start) > slowLoop {
		l.stalls.stalled()
	}
}

// watchThemes watches every registered theme source, again whenever they change: a plugin or the theme editor
// can add one at any time.
func (l *loop) watchThemes() {
	srcs, gen := l.reg.Themes()
	if gen == l.themeGen {
		return
	}
	l.unwatchThemes()
	for _, src := range srcs {
		l.unwatch = append(l.unwatch, src.Watch(l.retheme))
	}
	l.themeGen = gen
	l.themeDirty.Store(true)
}

func (l *loop) unwatchThemes() {
	for _, stop := range l.unwatch {
		stop()
	}
	l.unwatch = nil
}

// loadTheme resolves the theme ui.theme names, in ui.variant unless it has only one. If it doesn't resolve, the theme already
// showing stays; with none showing, the built-in one does.
func (l *loop) loadTheme() {
	r, err := chosenTheme(l.reg, l.osLight)
	if err != nil {
		l.log.Error("theme", "err", err)
		if l.theme != nil {
			return
		}
	}
	l.theme = r
	l.ui.SetTheme(r)
}

// chosenTheme is the theme ui.theme names, in ui.variant (the OS's when it's neither). If it doesn't resolve, it's
// the built-in one, with the error.
func chosenTheme(reg registry.Registry, osLight bool) (*theme.Resolved, error) {
	name, variant := "oriel", theme.Dark
	if v, _, err := reg.Options().Get("ui.theme", registry.At{}); err == nil {
		name = v.(string)
	}
	if v, _, err := reg.Options().Get("ui.variant", registry.At{}); err == nil {
		variant = theme.Variant(v.(string))
	}
	if variant != theme.Dark && variant != theme.Light {
		variant = theme.Dark
		if osLight {
			variant = theme.Light
		}
	}
	srcs, _ := reg.Themes()
	r, err := resolveTheme(srcs, name, variant)
	if err == nil {
		return r, nil
	}
	b, berr := resolveTheme(theme.Builtins(), "oriel", variant)
	if berr != nil {
		panic(berr) // the built-in theme always resolves; its tests say so
	}
	return b, fmt.Errorf("%s: %w", name, err)
}

func resolveTheme(sources []theme.Source, name string, v theme.Variant) (*theme.Resolved, error) {
	st, err := theme.Stack(sources, name)
	if err != nil {
		return nil, err
	}
	return theme.Resolve(st, theme.Effective(st, v))
}

// frameInterval paces redraws while something animates.
const frameInterval = time.Second / 120

// wake is when the loop next has work without input: a pending chord's timeout, the next animation
// frame (a frameInterval after the one drawn last), or a tooltip's delay.
func (l *loop) wake(drawn time.Time) time.Time {
	d := l.keys.Deadline()
	if u := l.ui.NextFrame(); !u.IsZero() {
		if soon := drawn.Add(frameInterval); u.Before(soon) {
			u = soon
		}
		if d.IsZero() || u.Before(d) {
			d = u
		}
	}
	return d
}

// handle feeds ev in. It reports whether the next frame can differ for it.
func (l *loop) handle(ctx context.Context, ev render.Event) bool {
	switch ev := ev.(type) {
	case render.PointerEvent:
		changed := l.ui.Pointer(ev)
		l.input.arrived(ev.At, changed)
		l.win.SetCursor(l.ui.Cursor())
		return changed
	case render.SlowFrameEvent:
		l.stalls.stalled()
		return false
	case render.AppearanceEvent:
		l.osLight = !ev.Dark
		l.themeDirty.Store(true)
	case render.DropEvent:
		l.sh.dropped(ev)
	case render.CloseRequestEvent:
		l.sh.requestQuit()
	case render.ModsEvent:
		l.ui.Mods(ev.Mods)
	case render.KeyEvent:
		l.ui.Chord()
		k := notation(ev)
		if p := l.keys.Pending(); len(p) > 0 && k == "<CR>" && l.keys.Complete() != nil && acceptKey(l.sh.cfg.Registry, l.keys.Modes(), p) {
			l.run(ctx, l.keys.Accept(time.Now()))
			return true
		}
		if p := l.keys.Pending(); len(p) > 0 && (k == "<BS>" || k == "<Esc>" || k == "?") {
			switch k {
			case "<BS>":
				l.keys.BackUp(time.Now())
			case "?":
				// Which-key's ?: every keymap under the prefix, in the keymaps picker.
				l.keys.Cancel()
				l.run(ctx, []registry.Action{{Keymap: &registry.Keymap{Command: "picker.open", Args: registry.Args{"source": "keymaps", "query": keymap.Format(p)}}}})
			default:
				l.keys.Cancel()
			}
			return true
		}
		l.run(ctx, l.keys.Feed(l.sh.keyModesFor(k), k, time.Now()))
	}
	return true
}

// acceptKey reports whether ⏎ after the pending keys runs their own mapping (which-key's ⏎), rather than going on
// to a mapping of its own.
func acceptKey(r registry.Registry, modes, pending []string) bool {
	exact, more := r.Lookup(modes, append(slices.Clone(pending), "<CR>"))
	return exact == nil && !more
}

// modes are the keymap modes keys resolve in, most specific first: the active key layers' (a popup's); then a
// focused widget's own mode (an input's
// "input", a list's "list"); or, with none, ViewKeyMode while a view in the layout has focus, or EmptyKeyMode while the
// main area is empty, then the editor's "n"; then "global", for app chords that work everywhere.
func (l *loop) modes() []string { return l.sh.keyModes() }

func (sh *shell) keyModes() []string {
	return append(registry.ActiveKeyModes(sh.cfg.Registry), sh.baseKeyModes()...)
}

// keyModesFor are the modes key resolves in: keyModes, without the global maps when it's a ⌃ key and what has focus
// takes those itself (registry.RawKeys), as a terminal does.
func (sh *shell) keyModesFor(key string) []string {
	modes := sh.keyModes()
	base := sh.baseKeyModes()
	if strings.HasPrefix(key, "<C-") && !strings.Contains(key, "D-") && !strings.Contains(key, "M-") &&
		registry.IsRawKeyMode(sh.cfg.Registry, base[0]) {
		return slices.DeleteFunc(modes, func(m string) bool { return m == "global" })
	}
	return modes
}

func (sh *shell) baseKeyModes() []string {
	if m := sh.ui.Load().KeyMode(); m != "" {
		return []string{m, "global"}
	}
	if sh.layout.root.Focus != nil {
		return []string{ViewKeyMode, "n", "global"}
	}
	if len(sh.layout.root.Shown()) == 0 {
		return []string{EmptyKeyMode, "n", "global"}
	}
	return []string{"n", "global"}
}

// run runs mapped commands, and gives unmapped keys to the focused widget.
func (l *loop) run(ctx context.Context, acts []registry.Action) {
	for _, a := range acts {
		if a.Keymap == nil {
			for _, k := range a.Keys {
				l.ui.Key(fromNotation(k))
			}
			continue
		}
		if k := a.Keymap; !l.ready(k) {
			if k.Desc != "" {
				l.store.Apply(core.Notified{Notice: core.Notice{ID: "command.unavailable." + k.Command, Severity: core.SevInfo, Source: "core",
					Text: k.Desc + " isn't available yet", Time: now()}})
			}
			continue
		}
		var err error
		l.timed(func() { err = l.reg.Run(ctx, a.Keymap.Command, a.Keymap.Args) })
		if err != nil {
			if _, ok := err.(*registry.Panic); !ok {
				l.store.Apply(core.Notified{Notice: commandNotice(l.reg, a.Keymap.Command, err, l.sh.cfg.ConfigDir)})
			}
		}
	}
}

// ready is false when k's command says it isn't available yet, as a rail entry's view before it's built; a command
// that's missing or turned off is ready, so running it says why it can't.
func (l *loop) ready(k *registry.Keymap) bool {
	for _, c := range l.reg.Commands() {
		if c.ID == k.Command && c.Available != nil {
			ok := true
			registry.Call(func() error { ok = c.Available(k.Args); return nil })
			return ok
		}
	}
	return true
}

func (l *loop) draw() error {
	if z, ok := l.win.(render.Zoomer); ok {
		z.SetZoom(zoomOf(l.reg.Options()))
	}
	size := l.win.Size()
	o := l.ui.Options()
	if on, _, err := l.reg.Options().Get("ui.reduced_motion", registry.At{}); err == nil {
		o.ReducedMotion = on == true
	}
	o.Bars = ui.BarsAuto
	if v, _, err := l.reg.Options().Get("ui.scrollbars", registry.At{}); err == nil && v == "always" {
		o.Bars = ui.BarsAlways
	}
	o.TooltipScale = partScale(l.reg.Options(), "ui.tooltip.font_size")
	l.ui.SetOptions(o)
	l.watchThemes()
	if l.themeDirty.Swap(false) {
		l.loadTheme()
	}
	if size.X <= 0 || size.Y <= 0 {
		return nil // the window hasn't its size yet: its ResizeEvent draws the first frame
	}
	defer func() { l.win.SetCursor(l.ui.Cursor()) }()
	return l.win.Frame(func(f render.Frame) {
		f.Rect(render.Rect{Max: size}, render.Style{Fill: l.theme.C(theme.BgBase)})
		start := time.Now()
		l.ui.Frame(f, size, func(c *ui.Ctx) {
			buildWindow(c, l.reg, size)
			if l.debug {
				frameOverlay(c, l.frameTime, l.input.drawn)
			}
		})
		l.frameTime = time.Since(start)
	})
}

func (l *loop) shot(th *theme.Resolved) *image.RGBA {
	if th == nil {
		th = l.theme
	}
	return shotOf(l.ui, l.reg, l.win.Size(), th)
}

// shotOf builds and paints the window again on a fork of u, in th, onto the CPU.
func shotOf(u *ui.UI, reg registry.Registry, size render.Point, th *theme.Resolved) *image.RGBA {
	f := u.Fork()
	f.SetTheme(th)
	var list render.List
	f.Frame(list.Record(1), size, func(c *ui.Ctx) { buildWindow(c, reg, size) })
	return raster.Render(&list, size, 1, th.C(theme.BgBase))
}

// buildWindow is the window: the title bar, the rail beside the root view, the status line under them, and the
// notifications and modal over it all.
func buildWindow(c *ui.Ctx, reg registry.Registry, size render.Point) {
	vc := registry.ViewCtx{Width: size.X, Height: size.Y}
	c.Column("shell", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		if v, ok := reg.ViewByID(TitleBarView); ok {
			c.Box(TitleBarView, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
		}
		c.Row("main", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			if v, ok := reg.ViewByID(RailView); ok {
				c.Box(RailView, ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
			}
			if v, ok := reg.ViewByID(RootView); ok {
				c.Box(RootView, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
			}
		})
		if v, ok := reg.ViewByID(StatusLineView); ok {
			c.Box(StatusLineView, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
		}
	})
	ids := []string{NotificationsView, ModalView}
	for _, id := range reg.Views() {
		if strings.HasPrefix(id, OverlayPrefix) {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		if v, ok := reg.ViewByID(id); ok {
			c.Box(id, ui.Style{W: ui.Px(0), H: ui.Px(0)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) }) // its content floats
		}
	}
}

// frameOverlay is --debug's frame time and how far the pointer was behind, at the bottom right.
func frameOverlay(c *ui.Ctx, d, behind time.Duration) {
	at := render.Point{X: c.Size().X - 8, Y: c.Size().Y - 30}
	c.Box("frametime", ui.Style{Pad: ui.PadXY(6, 2), Radius: 4, Fill: c.Theme().C(theme.BgOverlay), Z: ui.LayerTooltip,
		Float: &ui.Float{Point: &at, Self: render.Point{X: 1, Y: 1}}}, func(c *ui.Ctx) {
		c.Text(nil, fmt.Sprintf("frame %.2f ms · pointer %d ms", float64(d.Microseconds())/1000, behind.Milliseconds()), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10},
			Color: c.Theme().C(theme.Brand)})
	})
}

// fromNotation reads a keymap token back into a key press: "x", "<CR>", "<C-w>", "<D-S-s>".
func fromNotation(tok string) render.KeyEvent {
	if !strings.HasPrefix(tok, "<") || len(tok) < 3 {
		return render.KeyEvent{Key: tok}
	}
	body := tok[1 : len(tok)-1]
	var mods render.Mods
	for len(body) > 2 && body[1] == '-' {
		switch body[0] {
		case 'C':
			mods |= render.ModCtrl
		case 'M':
			mods |= render.ModAlt
		case 'D':
			mods |= render.ModSuper
		case 'S':
			mods |= render.ModShift
		}
		body = body[2:]
	}
	if toks := keymap.Parse("<" + body + ">"); len(toks) == 1 && utf8.RuneCountInString(toks[0]) == 1 {
		body = toks[0] // <Space> is " ", <lt> is "<"
	}
	return render.KeyEvent{Key: body, Mods: mods}
}

// notation writes a key press as a canonical keymap token: "x", "X", "<C-x>", "<D-s>", "<CR>".
func notation(ev render.KeyEvent) string {
	single := utf8.RuneCountInString(ev.Key) == 1
	mods := ev.Mods
	// A character's shift is in the character; Space's isn't, so ⌃⇧Space keeps it (⇧Space alone is still a space). A
	// control-letter's case is folded (⌃C is ⌃c), so ⌃⇧ and a letter keeps its shift too: <C-S-c>, Linux's copy.
	ctrlLetter := single && mods&render.ModShift != 0 && mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == render.ModCtrl &&
		unicode.IsLetter([]rune(ev.Key)[0])
	if ctrlLetter {
		ev.Key = strings.ToLower(ev.Key)
	}
	if single && !ctrlLetter && (ev.Key != " " || mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == 0) {
		mods &^= render.ModShift
	}
	if single && mods == 0 {
		return ev.Key
	}
	s := "<"
	for _, m := range []struct {
		mod    render.Mods
		prefix string
	}{{render.ModCtrl, "C-"}, {render.ModAlt, "M-"}, {render.ModSuper, "D-"}, {render.ModShift, "S-"}} {
		if mods&m.mod != 0 {
			s += m.prefix
		}
	}
	name := ev.Key
	if f := keymap.Format([]string{ev.Key}); single && f != ev.Key {
		name = f[1 : len(f)-1] // " " is <Space>, "<" is <lt>
	}
	if toks := keymap.Parse(s + name + ">"); len(toks) == 1 {
		return toks[0]
	}
	return s + name + ">"
}
