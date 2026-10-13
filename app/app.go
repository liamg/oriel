package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/ext"
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

type Config struct {
	Store      core.Store        // the materialised state the worker feeds; in-memory if nil
	Registry   registry.Registry // a fresh one if nil
	Extensions []ext.Extension   // activated in order, after the built-ins
	Logs       *Logs             // where log records go; a ring in memory if nil
	ConfigDir  string            // ~/.config/oriel unless set; user themes live in its themes/
	CrashDir   string            // crash reports are written to and offered from here; none if ""
	Version    string
	Desktop    Desktop  // the clipboard and the browser; the window's, or the OS's, if nil
	Debug      bool     // oriel --debug: the frame-time overlay
	Files      []string // open in the editor at start
	WorkDir    string   // where relative paths are; the current directory if ""
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

// Run opens a window and runs the app until the window closes or ctx is done.
func Run(ctx context.Context, be render.Backend, cfg Config) (err error) {
	if cfg.Store == nil {
		cfg.Store = eventstore.New()
	}
	if cfg.Registry == nil {
		cfg.Registry = registry.New()
	}
	if cfg.Logs == nil {
		cfg.Logs = NewLogs(LogOpts{})
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = DefaultConfigDir()
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	log := cfg.Logs.Logger("core")
	events := newEventTail(500)
	defer cfg.Store.Subscribe(core.Filter{}, events.add)()
	defer cfg.Store.Subscribe(core.Filter{}, func(ev core.Event) { logNotice(cfg.Store, log, ev) })()
	defer cfg.Registry.OnFault(func(f registry.Fault) { cfg.Store.Apply(core.Notified{Notice: faultNotice(f, cfg.Logs)}) }).Dispose()
	dirty := make(chan struct{}, 1)
	poke := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	sh := &shell{cfg: cfg, events: events, poke: poke}
	defer func() {
		if v := recover(); v != nil {
			err = sh.crashed(v, debug.Stack())
		}
	}()
	var active []ext.Extension
	defer func() {
		for _, e := range slices.Backward(active) {
			err = errors.Join(err, e.Deactivate(context.WithoutCancel(ctx)))
		}
	}()
	activate := func(e ext.Extension, source string) error {
		if err := e.Activate(ctx, sh.host(source)); err != nil {
			return fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
		active = append(active, e)
		return nil
	}
	for _, e := range builtins(sh) {
		if err := activate(e, "builtin:"+e.Manifest().ID); err != nil {
			return err
		}
	}
	for _, e := range cfg.Extensions {
		if err := activate(e, e.Manifest().ID); err != nil {
			return err
		}
	}

	w, err := be.NewWindow(render.WindowOpts{Title: "Oriel"})
	if err != nil {
		return err
	}
	defer w.Close()
	sh.desktop = cfg.Desktop
	if sh.desktop == nil {
		sh.desktop = windowDesktop(w)
	}

	cancel := cfg.Store.Subscribe(core.Filter{}, func(core.Event) { poke() })
	defer cancel()
	cfg.Logs.Ring.OnWrite(poke)

	l := &loop{sh: sh, debug: cfg.Debug, reg: cfg.Registry, store: cfg.Store, keys: registry.NewResolver(cfg.Registry), win: w, ui: ui.New(be.Shaper(), textStyle), log: log}
	l.ui.Title = "Oriel"
	sh.keys, sh.modes = l.keys, l.modes
	l.themeDirty.Store(true)
	retheme := func() { l.themeDirty.Store(true); poke() }
	for _, src := range cfg.Registry.Themes() {
		defer src.Watch(retheme)()
	}
	for _, opt := range []string{"ui.theme", "ui.variant"} {
		defer cfg.Registry.Options().Watch(opt, retheme)()
	}
	timer := time.NewTimer(0)
	<-timer.C
	var acts <-chan render.AccessAction
	accessible, _ := w.(render.Accessible)
	if accessible != nil {
		acts = accessible.AccessActions()
	}
	var tree *render.AccessNode
	for {
		if err := l.draw(); err != nil {
			return err
		}
		if accessible != nil {
			next := l.ui.AccessTree()
			if up := render.AccessDiff(tree, next); len(up.Changed) > 0 || len(up.Removed) > 0 || tree == nil {
				accessible.UpdateAccess(up)
			}
			tree = next
		}
		if d := l.wake(); !d.IsZero() {
			timer.Reset(time.Until(d))
		} else {
			timer.Stop()
		}
		select {
		case a := <-acts:
			l.ui.Act(ui.ID(a.ID), a.Action)
		case <-ctx.Done():
			return ctx.Err()
		case <-dirty:
		case now := <-timer.C:
			l.run(ctx, l.keys.Timeout(now))
			// and a redraw, for a spring or a tooltip that was due
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			l.handle(ctx, ev)
		}
	}
}

type host struct {
	registry.Registry
	store  core.Store
	source string
	logs   *Logs
	log    *slog.Logger
	sh     *shell
}

// host is what an extension registered as source sees.
func (sh *shell) host(source string) *host {
	return &host{Registry: registry.WithSource(sh.cfg.Registry, source), store: sh.cfg.Store, source: source, logs: sh.cfg.Logs,
		log: sh.cfg.Logs.Logger(source), sh: sh}
}

func (h *host) Layout() *layout.Root { return h.sh.layout.root }
func (h *host) Redraw()              { h.sh.poke() }

func (h *host) Pending() ext.Pending {
	if k := h.sh.keys; k != nil && len(k.Pending()) > 0 {
		return ext.Pending{Keys: k.Pending(), Modes: k.Modes(), Since: k.Since()}
	}
	if h.sh.enginePending != nil {
		return h.sh.enginePending()
	}
	return ext.Pending{}
}

func (h *host) PaneRect(v layout.ViewRef) (render.Rect, bool) {
	w := h.sh.layout.root.Find(v)
	if w == nil {
		return render.Rect{}, false
	}
	r, ok := h.sh.layout.rects[w.Leaf]
	return r, ok
}

func (h *host) Session(id core.SessionID) (core.Session, bool) { return h.store.Session(id) }
func (h *host) Sessions() []core.Session                       { return h.store.Sessions() }
func (h *host) Log() *slog.Logger                              { return h.log }
func (h *host) State() core.State                              { return h.store.State() }

func (h *host) Notify(n core.Notice) {
	if n.Source == "" {
		n.Source = h.source
	}
	if err := h.store.Apply(core.Notified{Notice: redactNotice(n, h.logs.Redact)}); err != nil {
		h.log.Error("notify", "id", n.ID, "err", err)
	}
}

type loop struct {
	themeDirty atomic.Bool
	theme      *theme.Resolved

	sh        *shell
	debug     bool
	frameTime time.Duration // the last frame's build and record, for the debug overlay

	reg   registry.Registry
	store core.Store
	keys  *registry.Resolver
	win   render.Window
	ui    *ui.UI
	log   *slog.Logger
}

// loadTheme resolves the theme ui.theme names, in ui.variant. If it doesn't resolve, the theme already
// showing stays; with none showing, the built-in one does.
func (l *loop) loadTheme() {
	name, variant := "oriel", theme.Dark
	if v, _, err := l.reg.Options().Get("ui.theme", registry.At{}); err == nil {
		name = v.(string)
	}
	if v, _, err := l.reg.Options().Get("ui.variant", registry.At{}); err == nil {
		variant = theme.Variant(v.(string))
	}
	r, err := resolveTheme(l.reg.Themes(), name, variant)
	if err != nil {
		l.log.Error("theme", "name", name, "err", err)
		if l.theme != nil {
			return
		}
		if r, err = resolveTheme(theme.Builtins(), "oriel", variant); err != nil {
			panic(err) // the built-in theme always resolves; its tests say so
		}
	}
	l.theme = r
	l.ui.SetTheme(r)
}

func resolveTheme(sources []theme.Source, name string, v theme.Variant) (*theme.Resolved, error) {
	st, err := theme.Stack(sources, name)
	if err != nil {
		return nil, err
	}
	return theme.Resolve(st, v)
}

// frameInterval paces redraws while something animates.
const frameInterval = time.Second / 120

// wake is when the loop next has work without input: a pending chord's timeout, the next animation
// frame, or a tooltip's delay.
func (l *loop) wake() time.Time {
	d := l.keys.Deadline()
	if u := l.ui.NextFrame(); !u.IsZero() {
		if soon := time.Now().Add(frameInterval); u.Before(soon) {
			u = soon
		}
		if d.IsZero() || u.Before(d) {
			d = u
		}
	}
	return d
}

func (l *loop) handle(ctx context.Context, ev render.Event) {
	switch ev := ev.(type) {
	case render.PointerEvent:
		l.ui.Pointer(ev)
	case render.KeyEvent:
		k := notation(ev)
		if len(l.keys.Pending()) > 0 && (k == "<BS>" || k == "<Esc>") {
			if k == "<BS>" {
				l.keys.BackUp(time.Now())
			} else {
				l.keys.Cancel()
			}
			return
		}
		l.run(ctx, l.keys.Feed(l.modes(), k, time.Now()))
	}
}

// run runs mapped commands. Unmapped keys go nowhere until there is an input engine (ED-2).
// modes are the keymap modes keys resolve in, most specific first: a focused widget's own mode (an input's
// "input", a list's "list"), or the editor's "n"; then "global", for app chords that work everywhere.
func (l *loop) modes() []string {
	if m := l.ui.KeyMode(); m != "" {
		return []string{m, "global"}
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
		if err := l.reg.Run(ctx, a.Keymap.Command, a.Keymap.Args); err != nil {
			if _, ok := err.(*registry.Panic); !ok {
				l.store.Apply(core.Notified{Notice: commandNotice(l.reg, a.Keymap.Command, err)})
			}
		}
	}
}

func (l *loop) draw() error {
	size := l.win.Size()
	if on, _, err := l.reg.Options().Get("ui.reduced_motion", registry.At{}); err == nil {
		o := l.ui.Options()
		o.ReducedMotion = on == true
		l.ui.SetOptions(o)
	}
	if l.themeDirty.Swap(false) {
		l.loadTheme()
	}
	defer func() { l.win.SetCursor(l.ui.Cursor()) }()
	return l.win.Frame(func(f render.Frame) {
		f.Rect(render.Rect{Max: size}, render.Style{Fill: l.theme.C(theme.BgBase)})
		start := time.Now()
		l.ui.Frame(f, size, func(c *ui.Ctx) {
			buildWindow(c, l.reg, size)
			if l.debug {
				frameOverlay(c, l.frameTime)
			}
		})
		l.frameTime = time.Since(start)
	})
}

// buildWindow is the window: the root view with the status line under it, and the notifications and modal
// over both.
func buildWindow(c *ui.Ctx, reg registry.Registry, size render.Point) {
	vc := registry.ViewCtx{Width: size.X, Height: size.Y}
	c.Column("shell", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		if v, ok := reg.ViewByID(TitleBarView); ok {
			c.Box(TitleBarView, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
		}
		if v, ok := reg.ViewByID(RootView); ok {
			c.Box(RootView, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(v.Build(vc)) })
		}
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

// frameOverlay is --debug's frame time, at the bottom right.
func frameOverlay(c *ui.Ctx, d time.Duration) {
	at := render.Point{X: c.Size().X - 8, Y: c.Size().Y - 30}
	c.Box("frametime", ui.Style{Pad: ui.PadXY(6, 2), Radius: 4, Fill: c.Theme().C(theme.BgOverlay), Z: 2000,
		Float: &ui.Float{Point: &at, Self: render.Point{X: 1, Y: 1}}}, func(c *ui.Ctx) {
		c.Text(nil, fmt.Sprintf("frame %.2f ms", float64(d.Microseconds())/1000), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10},
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
	if single {
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
