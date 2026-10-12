package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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
)

// RootView is the view that fills the window. Override it to replace the whole screen.
const RootView = "app.root"

type Config struct {
	Store      core.Store        // the materialised state the worker feeds; in-memory if nil
	Registry   registry.Registry // a fresh one if nil
	Extensions []ext.Extension   // activated in order, after the built-ins
	Log        *slog.Logger
	ConfigDir  string // ~/.config/oriel unless set; user themes live in its themes/
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
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if cfg.ConfigDir == "" {
		cfg.ConfigDir = DefaultConfigDir()
	}
	var active []ext.Extension
	defer func() {
		for _, e := range slices.Backward(active) {
			err = errors.Join(err, e.Deactivate(context.WithoutCancel(ctx)))
		}
	}()
	activate := func(e ext.Extension, source string) error {
		h := &host{Registry: registry.WithSource(cfg.Registry, source), store: cfg.Store, log: cfg.Log.With("ext", e.Manifest().ID)}
		if err := e.Activate(ctx, h); err != nil {
			return fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
		active = append(active, e)
		return nil
	}
	for _, e := range builtins(cfg) {
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

	dirty := make(chan struct{}, 1)
	poke := func() {
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
	cancel := cfg.Store.Subscribe(core.Filter{}, func(core.Event) { poke() })
	defer cancel()

	l := &loop{reg: cfg.Registry, keys: registry.NewResolver(cfg.Registry), win: w, ui: ui.New(be.Shaper(), textStyle), log: cfg.Log}
	l.ui.Title = "Oriel"
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
		if d := l.wake(); !d.IsZero() {
			timer.Reset(time.Until(d))
		} else {
			timer.Stop()
		}
	}
}

type host struct {
	registry.Registry
	store core.Store
	log   *slog.Logger
}

func (h *host) Session(id core.SessionID) (core.Session, bool) { return h.store.Session(id) }
func (h *host) Sessions() []core.Session                       { return h.store.Sessions() }
func (h *host) Log() *slog.Logger                              { return h.log }

type loop struct {
	themeDirty atomic.Bool
	theme      *theme.Resolved

	reg  registry.Registry
	keys *registry.Resolver
	win  render.Window
	ui   *ui.UI
	log  *slog.Logger
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
		l.run(ctx, l.keys.Feed(l.modes(), notation(ev), time.Now()))
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
			l.log.Error("command failed", "command", a.Keymap.Command, "err", err)
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
		l.ui.Frame(f, size, func(c *ui.Ctx) {
			v, ok := l.reg.ViewByID(RootView)
			if !ok {
				return
			}
			c.Box(RootView, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(16)}, func(c *ui.Ctx) {
				c.Node(v.Build(registry.ViewCtx{Width: size.X, Height: size.Y}))
			})
		})
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
