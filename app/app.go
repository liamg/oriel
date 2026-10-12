package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

// RootView is the view that fills the window. Override it to replace the whole screen.
const RootView = "app.root"

type Config struct {
	Store      core.Store        // the materialised state the worker feeds; in-memory if nil
	Registry   registry.Registry // a fresh one if nil
	Extensions []ext.Extension   // activated in order, after the built-ins
	Log        *slog.Logger
}

var (
	background = render.Color{R: 0x06, G: 0x06, B: 0x06, A: 0xff}
	textStyle  = ui.TextStyle{
		Font:  typeset.FontSpec{Family: typeset.Mono, Size: 13},
		Color: render.Color{R: 0xec, G: 0xe6, B: 0xd8, A: 0xff},
	}
)

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
	for _, e := range builtins() {
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
	cancel := cfg.Store.Subscribe(core.Filter{}, func(core.Event) {
		select {
		case dirty <- struct{}{}:
		default:
		}
	})
	defer cancel()

	l := &loop{reg: cfg.Registry, keys: registry.NewResolver(cfg.Registry), win: w, ui: ui.New(be.Shaper(), textStyle), log: cfg.Log, modes: []string{"n"}}
	timer := time.NewTimer(0)
	<-timer.C
	for {
		if err := l.draw(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-dirty:
		case now := <-timer.C:
			l.run(ctx, l.keys.Timeout(now))
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			l.handle(ctx, ev)
		}
		if d := l.keys.Deadline(); !d.IsZero() {
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
	reg   registry.Registry
	keys  *registry.Resolver
	win   render.Window
	ui    *ui.UI
	log   *slog.Logger
	modes []string // the focused mode first
}

func (l *loop) handle(ctx context.Context, ev render.Event) {
	switch ev := ev.(type) {
	case render.KeyEvent:
		l.run(ctx, l.keys.Feed(l.modes, notation(ev), time.Now()))
	}
}

// run runs mapped commands. Unmapped keys go nowhere until there is an input engine (ED-2).
func (l *loop) run(ctx context.Context, acts []registry.Action) {
	for _, a := range acts {
		if a.Keymap == nil {
			continue
		}
		if err := l.reg.Run(ctx, a.Keymap.Command, a.Keymap.Args); err != nil {
			l.log.Error("command failed", "command", a.Keymap.Command, "err", err)
		}
	}
}

func (l *loop) draw() error {
	size := l.win.Size()
	return l.win.Frame(func(f render.Frame) {
		f.Rect(render.Rect{Max: size}, render.Style{Fill: background})
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
