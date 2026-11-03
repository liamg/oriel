package shell

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/edit"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/search"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The views the window is made of. Each is an ordinary view: override one to replace that part of the screen.
const (
	NotificationsView = "app.notifications" // toasts, floating over everything
	ModalView         = "app.modal"         // when Oriel can't go on without an answer
)

// ViewKeyMode is the keymap mode of a view in the layout that has focus but no keys of its own: q closes it.
// EmptyKeyMode is the mode while the main area is empty and shows the start view or the project overview.
const (
	ViewKeyMode  = "view"
	EmptyKeyMode = "empty"
)

// Config is the window's: what the shell reads of the app's config.
type Config struct {
	Store     core.Store      // the materialised state, shared with the session host; Host's, else in-memory, if nil
	Registry  uireg.Registry  // a fresh one if nil
	Plugins   []plugin.Plugin // activated in order, after the built-ins
	Logs      *Logs           // where log records go; a ring in memory if nil
	ConfigDir string          // DefaultConfigDir() unless set; user themes live in its themes/
	CrashDir  string          // crash reports are written to and offered from here; none if ""
	Version   string
	Desktop   Desktop  // the clipboard and the browser; the window's, or the OS's, if nil
	Debug     bool     // the frame-time overlay
	Trace     string   // the execution trace of the seconds before a stall, written here
	Files     []string // open in the editor at start
	WorkDir   string   // the directory open in the window, where relative paths are; none if "" (the start view shows)
	StateDir  string   // state that isn't config, such as the recent directories; logs.StateDir() if ""
	CacheDir  string   // compiled grammars and the like; the user's cache directory's oriel/ if ""
	// Host is the session host, in this process; no sessions if nil.
	Host *host.Host
	// Started is called once the window is open and every plugin active.
	Started func()
	// Activated is called with each plugin, built-ins first, once it's active: how a harness finds them.
	Activated func(plugin.Plugin)
	// Net is what plugins' HTTP goes through (http.request); a client of its own if nil.
	Net *http.Client
	// ImageNet is what markdown's remote images are fetched with; one that dials public addresses only if nil.
	ImageNet *http.Client
	// FSWatch starts the OS's file watchers; fswatch.New if nil.
	FSWatch func() (*fswatch.Watcher, error)
	// Secrets are the named secrets plugins' requests carry; secrets.json in StateDir if nil.
	Secrets plugin.Secrets
	// Quit asks the window to quit as its close button does, without asking about scheduled jobs.
	Quit <-chan struct{}
}

// Builtins are the window's built-in plugins in the order they start, made each time it starts its plugins.
type Builtins func(Parts) []plugin.Plugin

// Parts are what the shell gives Builtins: its config as it is now (the directory changes as the window opens
// again), its own plugins, and what it keeps for the features.
type Parts struct {
	Config    Config
	Clipboard edit.Clipboard // the system clipboard, as the editor's registers see it
	Pictures  *kit.Pictures  // the window's images
	Tools     *search.Tools  // the machine's search tools

	UI, Frames, Themes, Layout, Rail, Start, Overview, Introspect, StatusLine, SessionTabs plugin.Plugin
	Omnibox, Picker, WhichKey, Help, Nav, Menus, Notices, Desktop, Quit                    plugin.Plugin
}

var textStyle = ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 13}}

// DefaultConfigDir is $XDG_CONFIG_HOME/oriel, or ~/.config/oriel, on every platform.
func DefaultConfigDir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "oriel")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "oriel")
}

// Run opens a window and runs the app, builtins first, until the window closes or ctx is done.
func Run(ctx context.Context, be render.Backend, cfg Config, builtins Builtins) (err error) {
	cfg.defaults()
	cfg.windowDefaults()
	log := cfg.Logs.Logger("core")
	dirty := make(chan struct{}, 1)
	poke := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	sh, unsubscribe := newShell(cfg, builtins, poke)
	defer unsubscribe()
	probed := make(chan struct{}) // rg, off the loop, so the search panel finds the answer waiting
	go func() { sh.tools.Ripgrep(); close(probed) }()
	defer func() { <-probed }()
	sh.recents = &recents{path: filepath.Join(cfg.StateDir, "recent.json")}
	if cfg.WorkDir != "" {
		sh.recents.add(cfg.WorkDir, time.Now())
	}
	defer func() {
		if v := recover(); v != nil {
			err = sh.crashed(v, debug.Stack())
		}
	}()
	p := &running{sh: sh, ctx: ctx}
	defer func() {
		err = errors.Join(err, p.deactivate(p.ctx))
		sh.teardowns.Wait()
		kit.ForgetLanguages(cfg.Registry)
	}()
	if err := p.activate(); err != nil {
		return err
	}
	sh.openDirect(ctx, log)

	place := windowPlace{path: filepath.Join(cfg.StateDir, "window.json")}
	first, _ := chosenTheme(cfg.Registry, false)
	// Quitting hands the agents over first, so a close is held from the start.
	w, err := be.NewWindow(render.WindowOpts{Title: "Oriel", Width: 1280, Height: 800, Frame: place.load(), Background: first.C(theme.BgBase),
		HoldClose: cfg.Host != nil})
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
	if _, ok := w.(render.CloseHolder); ok && cfg.Host != nil {
		sh.holdsClose = true
	}
	sh.desktop = cfg.Desktop
	if sh.desktop == nil {
		sh.desktop = windowDesktop(w)
	}

	cancel := cfg.Store.Subscribe(core.Filter{}, func(core.Event) { poke() })
	defer cancel()
	cfg.Logs.OnShownWrite(poke)

	l := &loop{sh: sh, debug: cfg.Debug, reg: cfg.Registry, store: cfg.Store, keys: registry.NewResolver(cfg.Registry), win: w, ui: ui.New(be.Shaper(), textStyle), log: log}
	l.stalls = startStallTrace(cfg.Trace, log)
	defer l.stalls.stop()
	defer l.unwatchThemes()
	for _, stop := range l.open(poke) {
		defer stop()
	}
	if cfg.Started != nil {
		sh.todoMu.Lock()
		sh.todo = append(sh.todo, cfg.Started)
		sh.todoMu.Unlock()
	}
	defer sh.relaunchAfter(&err)
	return l.serve(ctx, p, dirty)
}

// windowDefaults are the directories Run fills in that a headless app goes without.
func (cfg *Config) windowDefaults() {
	if cfg.CacheDir == "" {
		if d, err := os.UserCacheDir(); err == nil {
			cfg.CacheDir = filepath.Join(d, "oriel")
		}
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = DefaultConfigDir()
	}
	if cfg.StateDir == "" {
		cfg.StateDir = logs.StateDir()
	}
}

// openDirect opens the window's directory as a direct session, unless the session in front holds it.
func (sh *shell) openDirect(ctx context.Context, log *slog.Logger) {
	cfg := sh.cfg
	st := sh.store.State()
	if cfg.Host == nil || cfg.WorkDir == "" || !cfg.Registry.Available("session.open-dir", nil) {
		return
	}
	if se, ok := st.Session(st.Focus); !ok || !kit.Holds(se.Snapshot(), cfg.WorkDir) {
		if err := cfg.Registry.Run(ctx, "session.open-dir", registry.Args{"dir": cfg.WorkDir}); err != nil {
			log.Error("direct session", "dir", cfg.WorkDir, "err", err)
		}
	}
}

// running are the plugins the window runs, started again each time it opens on another directory.
type running struct {
	sh     *shell
	ctx    context.Context
	active []plugin.Plugin
}

func (p *running) activate() error {
	_, err := p.sh.activate(p.ctx, &p.active)
	return err
}

func (p *running) deactivate(ctx context.Context) (err error) {
	for _, e := range slices.Backward(p.active) {
		err = errors.Join(err, e.Deactivate(context.WithoutCancel(ctx)))
	}
	p.active = nil
	return err
}

// reopen starts every plugin again on to's directory: Open Directory, or another session in front.
func (p *running) reopen(to reopenTo, log *slog.Logger) error {
	sh := p.sh
	sh.heading.Store(nil)
	if err := p.deactivate(plugin.WithTeardowns(p.ctx, &sh.teardowns)); err != nil {
		log.Error("reopen", "dir", to.dir, "err", err)
	}
	sh.cfg.WorkDir, sh.cfg.Files, sh.openedFor = to.dir, nil, to.session
	if to.session == "" && to.dir != "" {
		sh.recents.add(to.dir, time.Now())
	}
	return p.activate()
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
		cfg.Registry = uireg.New()
	}
	if cfg.Logs == nil {
		cfg.Logs = NewLogs(LogOpts{})
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
}

// provide gives u's widgets what the window keeps for them: its registry, for contributed icons, and its reads and pictures.
func (sh *shell) provide(u *ui.UI) {
	ui.Provide[registry.Registry](u, sh.cfg.Registry)
	ui.Provide(u, sh.reads)
	ui.Provide(u, sh.reads.Pictures)
	sh.widgets.apply(u)
}

// newShell is the shell over cfg, keeping the store's recent events and logging its notices until unsubscribed.
func newShell(cfg Config, builtins Builtins, poke func()) (*shell, func()) {
	events := newEventTail(500)
	log := cfg.Logs.Logger("core")
	tail := cfg.Store.Subscribe(core.Filter{}, events.add)
	notices := cfg.Store.Subscribe(core.Filter{}, func(ev core.Event) { logNotice(cfg.Store, log, ev) })
	sh := &shell{cfg: cfg, builtins: builtins, store: cfg.Store, events: events, poke: poke, reopen: make(chan reopenTo, 1), focus: newWindowFocus()}
	sh.reads = kit.NewReads(kit.ReadsOpts{Client: cfg.ImageNet, Wake: poke, Watch: cfg.FSWatch, Frame: func() int {
		if u := sh.ui.Load(); u != nil {
			return u.Frames()
		}
		return 0
	}, Now: func() time.Time {
		if u := sh.ui.Load(); u != nil {
			return u.Now()
		}
		return time.Now()
	}})
	sh.polls, sh.tools = &kit.PollSet{}, search.NewTools()
	sh.kept = newKeptStore(cfg.StateDir, log)
	gone := cfg.Store.Subscribe(core.Filter{}, func(ev core.Event) {
		if ev, ok := ev.(core.SessionClosed); ok {
			sh.kept.forget(ev.ID)
		}
	})
	services := window{sh}.contribute()
	return sh, func() {
		tail()
		notices()
		gone()
		services()
		sh.reads.Close()
		sh.kept.flush()
	}
}

// activate starts the built-ins, then cfg.Plugins, each on a host of its own, adding each to active as it starts: a
// lazy one waits for its first use.
// It answers the last built-in's host.
func (sh *shell) activate(ctx context.Context, active *[]plugin.Plugin) (*pluginHost, error) {
	sh.active = active
	var last *pluginHost
	for _, e := range sh.builtins(sh.parts()) {
		last = sh.host("builtin:" + e.Manifest().ID)
		if err := sh.start(ctx, e, last, active); err != nil {
			return nil, fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
	}
	for _, e := range sh.cfg.Plugins {
		if err := sh.start(ctx, e, sh.host(e.Manifest().ID), active); err != nil {
			return nil, fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
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
