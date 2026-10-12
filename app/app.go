package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
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
	textStyle  = ui.Style{
		Font: typeset.FontSpec{Family: "JetBrains Mono", Size: 13},
		FG:   render.Color{R: 0xec, G: 0xe6, B: 0xd8, A: 0xff},
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
	h := &host{Registry: cfg.Registry, store: cfg.Store, log: cfg.Log}

	exts := append(builtins(), cfg.Extensions...)
	var active []ext.Extension
	defer func() {
		for _, e := range slices.Backward(active) {
			err = errors.Join(err, e.Deactivate(context.WithoutCancel(ctx)))
		}
	}()
	for _, e := range exts {
		if err := e.Activate(ctx, h); err != nil {
			return fmt.Errorf("activate %s: %w", e.Manifest().ID, err)
		}
		active = append(active, e)
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

	l := &loop{reg: cfg.Registry, win: w, shaper: be.Shaper(), log: cfg.Log, mode: "n"}
	for {
		if err := l.draw(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-dirty:
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
	store core.Store
	log   *slog.Logger
}

func (h *host) Session(id core.SessionID) (core.Session, bool) { return h.store.Session(id) }
func (h *host) Sessions() []core.Session                       { return h.store.Sessions() }
func (h *host) Log() *slog.Logger                              { return h.log }

type loop struct {
	reg    registry.Registry
	win    render.Window
	shaper typeset.Shaper
	log    *slog.Logger
	mode   string
}

func (l *loop) handle(ctx context.Context, ev render.Event) {
	switch ev := ev.(type) {
	case render.KeyEvent:
		km, ok := l.reg.Key(l.mode, notation(ev))
		if !ok {
			return
		}
		if err := l.reg.Run(ctx, km.Command, km.Args); err != nil {
			l.log.Error("command failed", "command", km.Command, "err", err)
		}
	}
}

func (l *loop) draw() error {
	size := l.win.Size()
	return l.win.Frame(func(f render.Frame) {
		f.Rect(render.Rect{Max: size}, render.Style{Fill: background})
		if v, ok := l.reg.ViewByID(RootView); ok {
			ui.Draw(f, l.shaper, textStyle, v.Build(registry.ViewCtx{Width: size.X, Height: size.Y}), render.Point{X: 16, Y: 16})
		}
	})
}

// notation writes a key press the way keymaps name it: "x", "X", "<C-x>", "<D-s>".
func notation(ev render.KeyEvent) string {
	mods := ev.Mods
	if utf8.RuneCountInString(ev.Key) == 1 {
		mods &^= render.ModShift
	}
	if mods == 0 && utf8.RuneCountInString(ev.Key) == 1 {
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
	return s + ev.Key + ">"
}
