package shell

import (
	"context"
	"fmt"
	"image"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/internal/fsx"
	"github.com/liamg/oriel/render/raster"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

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
	built     time.Time     // when the last frame was built, not only painted again

	reg   uireg.Registry
	store core.Store
	keys  *registry.Resolver
	win   render.Window
	ui    *ui.UI
	log   *slog.Logger

	input  inputLag
	stalls *stallTrace
	hidden bool // the window can't be seen: no frames are drawn
	dog    *watchdog
}

// open readies the loop on its window: the clipboard, the UI the plugins see, the theme and the window's events.
// It answers what to stop as the window closes.
func (l *loop) open(poke func()) []func() {
	sh, reg := l.sh, l.reg
	l.ui.SetClipboard(sh.desktop)
	sh.provide(l.ui)
	l.ui.Title = "Oriel"
	sh.keys, sh.modes = l.keys, l.modes
	sh.ui.Store(l.ui)
	l.keys.Raw = func() bool {
		ed, ok := registry.EditorsOf(reg)
		return ok && len(ed.Pending().Keys) > 0
	}
	sh.theme, sh.shot = func() *theme.Resolved { return l.theme }, l.shot
	l.themeDirty.Store(true)
	l.retheme = func() { l.themeDirty.Store(true); poke() }
	l.themeGen = -1
	var stops []func()
	for _, opt := range []string{"ui.theme", "ui.variant"} {
		stops = append(stops, reg.Options().Watch(opt, l.retheme))
	}
	for _, e := range []registry.EventInfo{
		{Name: "FocusGained", Doc: "The window came to the front.", Payload: WindowFocus{}, Fired: true},
		{Name: "FocusLost", Doc: "Another app came in front of the window.", Payload: WindowFocus{}, Fired: true},
	} {
		stops = append(stops, reg.DeclareEvent(e).Dispose)
	}
	return stops
}

// accessWanted says a screen reader may be reading a: no tree is built for a window that knows none is. The last tree
// sent is kept, so the first update once one is reading brings the bridge up to date.
func accessWanted(a render.Accessible) bool {
	w, ok := a.(render.AccessWanted)
	return !ok || w.AccessWanted()
}

// turn is what the loop keeps from one turn to the next.
type turn struct {
	accessible render.Accessible
	tree       *render.AccessNode
	held       render.Event // read past a run of pointer moves; handled on the next turn
	pace       ui.Pacer
	after      []render.Event // input that waits for the frame owed, so it goes to what that frame shows
}

// serve runs the loop until the window closes or ctx is done: a frame when something changed, then the next input.
func (l *loop) serve(ctx context.Context, p *running, dirty <-chan struct{}) error {
	sh, w := l.sh, l.win
	timer := time.NewTimer(0)
	<-timer.C
	if v := sh.cfg.Version; v == "" || v == "dev" || l.debug {
		l.dog = startWatchdog(l.log, slowLoop)
		defer l.dog.stop()
	}
	var t turn
	t.pace.Ask()
	var acts <-chan render.AccessAction
	t.accessible, _ = w.(render.Accessible)
	if t.accessible != nil {
		acts = t.accessible.AccessActions()
	}
	for {
		drawn, err := l.frame(ctx, &t, dirty)
		if err != nil {
			return err
		}
		// What waited for the frame: the first goes in now, whatever else has asked for a frame meanwhile.
		for first := drawn; len(t.after) > 0 && (first || !l.owed(&t, dirty)); first = false {
			ev := t.after[0]
			t.after = t.after[1:]
			l.feedAndAsk(ctx, &t, ev)
		}
		l.arm(timer, &t)
		if t.held != nil {
			ev := t.held
			t.held = nil
			l.deferOrFeed(ctx, &t, ev, dirty)
			continue
		}
		select {
		case a := <-acts:
			l.ui.Act(a)
			t.pace.Ask()
		case <-ctx.Done():
			return ctx.Err()
		case <-sh.cfg.Quit:
			sh.quit(false)
		case <-dirty:
			t.pace.Ask()
		case to := <-sh.reopen:
			// the opening cover first, then the plugins start again behind it
			if err := l.draw(); err != nil {
				return err
			}
			if err := p.reopen(to, l.log); err != nil {
				return err
			}
			l.themeDirty.Store(true)
			t.pace.Ask()
		case now := <-timer.C:
			acts := l.keys.Timeout(now)
			l.run(ctx, acts)
			l.ask(&t, len(acts) == 0, now) // and a frame, for a spring, a tooltip or a loop's step that was due
		case ev, ok := <-w.Events():
			if !ok {
				return nil
			}
			ev, t.held = lastMove(ev, w.Events())
			l.deferOrFeed(ctx, &t, ev, dirty)
		}
	}
}

// takeWaiting takes the input already waiting, before a new frame is built: it was made on the frame on screen, so a
// click goes to what it was aimed at there. It reports false once the window's closed.
func (l *loop) takeWaiting(ctx context.Context, t *turn, dirty <-chan struct{}) bool {
	events := l.win.Events()
	for n := len(events); n > 0 || t.held != nil; n-- {
		ev := t.held
		t.held = nil
		if ev == nil {
			select {
			case next, ok := <-events:
				if !ok {
					return false
				}
				ev, t.held = lastMove(next, events)
			default:
				return true // the moves read past were the rest
			}
		}
		l.deferOrFeed(ctx, t, ev, dirty)
	}
	return true
}

// deferOrFeed feeds ev in (feedAndAsk), or, when it's a key and a frame is owed, keeps it until that frame is
// drawn: a key goes to what has focus in the frame drawn before it, as the one typing it saw, while the pointer goes
// to what's on screen.
// What comes after a kept event waits behind it.
func (l *loop) deferOrFeed(ctx context.Context, t *turn, ev render.Event, dirty <-chan struct{}) {
	if len(t.after) > 0 || l.waitsForFrame(ev) && l.owed(t, dirty) {
		t.after = append(t.after, ev)
		return
	}
	l.feedAndAsk(ctx, t, ev)
}

// owed says a frame has been asked for and not drawn, a hidden window's aside.
func (l *loop) owed(t *turn, dirty <-chan struct{}) bool {
	select {
	case <-dirty:
		t.pace.Ask()
	default:
	}
	_, at := t.pace.Due(time.Now())
	return !at.IsZero() && !l.hidden
}

// waitsForFrame says ev goes in after the frame owed: a key or a drop, and a release that ends a drag the pointer
// moved since the last frame, so what's dragged follows it all the way.
func (l *loop) waitsForFrame(ev render.Event) bool {
	switch ev := ev.(type) {
	case render.KeyEvent, render.DropEvent:
		return true
	case render.PointerEvent:
		return ev.Kind == render.PointerRelease && l.ui.Dragged()
	}
	return false
}

// frame runs what the loop was handed and takes the input waiting, draws a frame when one was asked for and the
// pacer lets it, unless the window is hidden, and runs keys typed behind ones the editor has now taken.
func (l *loop) frame(ctx context.Context, t *turn, dirty <-chan struct{}) (drawn bool, err error) {
	sh := l.sh
	l.timed(sh.runTodo)
	if !l.takeWaiting(ctx, t, dirty) {
		return false, nil // closed: the loop sees it next
	}
	if due, _ := t.pace.Due(time.Now()); !due || l.hidden {
		return false, nil
	}
	if !t.pace.Full() {
		l.timed(func() { err = l.repaint() })
		if err != nil {
			return false, err
		}
		t.pace.Drew(time.Now())
		sh.frames.Add(time.Now(), l.frameTime, 0, l.ui.WokenBy())
		return true, nil
	}
	sh.seeNav(l.reg, l.ui.Now())
	l.timed(func() { err = l.draw() })
	if err != nil {
		return false, err
	}
	t.pace.Drew(time.Now())
	sh.framed.Fire()
	moved := !l.input.oldest.IsZero()
	if l.input.built() {
		l.stalls.stalled()
	}
	lag := time.Duration(0)
	if moved {
		lag = l.input.drawn
	}
	sh.frames.Add(time.Now(), l.frameTime, lag, l.ui.WokenBy())
	// Keys typed behind ones the editor has taken in the frame: looked up, or passed on raw while it's mid-command.
	if acts := l.keys.Pump(sh.keyModes(), time.Now()); len(acts) > 0 || l.keys.Queued() {
		l.run(ctx, acts)
		sh.poke()
	}
	if t.accessible != nil && accessWanted(t.accessible) {
		next := l.ui.AccessTree()
		if up := render.AccessDiff(t.tree, next); len(up.Changed) > 0 || len(up.Removed) > 0 || t.tree == nil {
			t.accessible.UpdateAccess(up)
		}
		t.tree = next
	}
	return true, nil
}

// arm sets timer for when the loop next has work without input: a pending chord's timeout, a frame the UI wants
// (a spring, a tooltip's delay) or one the pacer is holding; or stops it. A hidden window wants no frames.
func (l *loop) arm(timer *time.Timer, t *turn) {
	now := time.Now()
	d := l.keys.Deadline()
	if u := l.ui.NextFrame(); !u.IsZero() && !l.hidden {
		if u.After(now) {
			d = earliest(d, u)
		} else {
			l.ask(t, true, now)
		}
	}
	if _, at := t.pace.Due(now); !at.IsZero() && !l.hidden {
		d = earliest(d, at)
	}
	if d.IsZero() {
		timer.Stop()
		return
	}
	timer.Reset(time.Until(d))
}

// ask asks for the frame the UI wants at now: painted again when it only steps decorative loops and paint is all that
// changed, built otherwise, and built anyway once a repaint has stood for longest.
func (l *loop) ask(t *turn, paintOnly bool, now time.Time) {
	if paintOnly && l.ui.Repaintable(now) && now.Sub(l.built) < repaintLongest {
		t.pace.AskRepaint()
		return
	}
	t.pace.Ask()
}

// repaintLongest is the longest a frame's build stands while loops repaint it: what changed without asking for a
// frame shows by then.
const repaintLongest = 500 * time.Millisecond

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}

// feedAndAsk dispatches ev, asking for a frame when it changed what one shows: a pointer move often doesn't.
func (l *loop) feedAndAsk(ctx context.Context, t *turn, ev render.Event) {
	var changed bool
	l.timed(func() { changed = l.dispatch(ctx, ev) })
	if changed {
		t.pace.Ask()
	}
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

// timed runs fn, a turn's work, and traces a stall when it holds the loop up past slowLoop; in a dev build the
// watchdog logs where it's held up meanwhile. The work is in a frame for fsx's check.
func (l *loop) timed(fn func()) {
	start := time.Now()
	l.dog.enter()
	defer l.dog.leave()
	defer fsx.Enter()()
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

// dispatch applies ev to the window. It reports whether the next frame can differ for it.
func (l *loop) dispatch(ctx context.Context, ev render.Event) bool {
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
		l.sh.quit(true)
	case render.VisibleEvent:
		l.hidden = !ev.Visible
		l.ui.SetWindow(l.sh.focus.Focused(), ev.Visible)
	case render.FocusEvent:
		l.ui.SetWindow(ev.Focused, !l.hidden)
		if l.sh.focus.set(ev.Focused) {
			name := map[bool]string{true: "FocusGained", false: "FocusLost"}[ev.Focused]
			l.reg.Fire(ctx, name, "", &WindowFocus{Focused: ev.Focused})
		}
	case render.ModsEvent:
		l.ui.Mods(ev.Mods)
	case render.KeyEvent:
		l.ui.KeyDown()
		k := kit.Notation(ev)
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
				l.ui.Key(kit.KeyEvent(k))
			}
			continue
		}
		if k := a.Keymap; !l.ready(k) {
			if k.Desc != "" {
				l.store.Apply(core.Notified{Notice: core.Notice{ID: "command.unavailable." + k.Command, Severity: core.SevInfo, Source: "core",
					Text: k.Desc + " isn't available yet", Time: l.sh.now()}})
			}
			continue
		}
		var err error
		l.timed(func() { err = l.reg.Run(ctx, a.Keymap.Command, a.Keymap.Args) })
		if err != nil {
			if _, ok := err.(*registry.Panic); !ok {
				l.store.Apply(core.Notified{Notice: commandNotice(l.reg, a.Keymap.Command, err, l.sh.cfg.ConfigDir, l.sh.now())})
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
	o.TooltipScale = kit.PartScale(l.reg.Options(), "ui.tooltip.font_size")
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
			buildWindow(c, l.reg, size, frameWindow(l.store.State(), l.sh.cfg.WorkDir))
			if l.debug {
				frameOverlay(c, l.frameTime, l.input.drawn)
			}
		})
		l.frameTime = time.Since(start)
		l.built = start
	})
}

// repaint paints the last frame's layout again, for decorative loops' next steps.
func (l *loop) repaint() error {
	size := l.win.Size()
	return l.win.Frame(func(f render.Frame) {
		f.Rect(render.Rect{Max: size}, render.Style{Fill: l.theme.C(theme.BgBase)})
		start := time.Now()
		l.ui.Repaint(f)
		l.frameTime = time.Since(start)
	})
}

func (l *loop) shot(th *theme.Resolved) *image.RGBA {
	if th == nil {
		th = l.theme
	}
	return shotOf(l.ui, l.reg, l.win.Size(), th, frameWindow(l.store.State(), l.sh.cfg.WorkDir))
}

// shotOf builds and paints the window again on a fork of u, in th, onto the CPU.
func shotOf(u *ui.UI, reg uireg.Registry, size render.Point, th *theme.Resolved, win uireg.Window) *image.RGBA {
	f := u.Fork()
	f.SetTheme(th)
	var list render.List
	f.Frame(list.Record(1), size, func(c *ui.Ctx) { buildWindow(c, reg, size, win) })
	return raster.Render(&list, size, 1, th.C(theme.BgBase))
}

// frameOverlay is --debug's frame time and how far the pointer was behind, at the bottom right.
func frameOverlay(c *ui.Ctx, d, behind time.Duration) {
	at := widget.AreaFloat(c, widget.AreaMain, render.Point{X: 1, Y: 1}, render.Point{X: 1, Y: 1}, render.Point{X: -8, Y: -4})
	c.Box("frametime", ui.Style{Pad: ui.PadXY(6, 2), Radius: 4, Fill: c.Theme().C(theme.BgOverlay), Z: ui.LayerTooltip,
		Float: at}, func(c *ui.Ctx) {
		c.Text(nil, fmt.Sprintf("frame %.2f ms · pointer %d ms", float64(d.Microseconds())/1000, behind.Milliseconds()), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10},
			Color: c.Theme().C(theme.Brand)})
	})
}
