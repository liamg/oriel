package stats

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The HUD: hold ⌥ to pull a drawer of panels down over anything, release to put it away, click to pin
// it. Its panels are indicators in the "hud" slot, and the same indicators fill the title bar's and the status line's
// slots (uireg.Indicator): the built-ins' and plugins' alike.
const (
	HUDView         = kit.OverlayPrefix + "hud"
	HUDKeyMode      = "hud"
	HoldOption      = "hud.hold"
	HoldDelayOption = "hud.hold_delay"
	hudColumns      = 4
)

var (
	tHUDKey   = theme.Declare("hud.key", "fg.muted")
	tHUDKeyOn = theme.Declare("hud.key.on", "accent")
)

var holdMods = map[string]render.Mods{"alt": render.ModAlt, "ctrl": render.ModCtrl, "shift": render.ModShift, "super": render.ModSuper}

var modGlyphs = map[render.Mods]string{render.ModAlt: "⌥", render.ModCtrl: "⌃", render.ModShift: "⇧", render.ModSuper: "⌘"}

// HUD is the HUD's plugin: its drawer, its key and the indicators service every bar draws its indicators with.
type HUD struct {
	kit.Disposer
	h plugin.Host

	held   bool // pulled down by holding the hold key, until it's let go
	pinned bool // stays until closed

	mu    sync.Mutex
	gen   int // the registry's contributions when the cache was built
	cache map[string]*builtIndicator
}

// builtIndicator is an indicator's node as last built, kept until it's built again.
type builtIndicator struct {
	node  ui.Node
	err   error
	at    time.Time
	dirty bool
}

func (x *HUD) Manifest() plugin.Manifest { return plugin.Manifest{ID: "hud"} }

func (x *HUD) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.cache = map[string]*builtIndicator{}
	x.Add(h.Contribute(registry.Service{Name: kit.IndicatorsService, Impl: x, Source: "hud"}))
	x.Add(h.Option(registry.OptionSpec{Name: HoldOption, Type: registry.Enum, Values: []string{"alt", "ctrl", "shift", "super", "off"}, Default: "alt",
		Scopes: registry.ScopeUser, Help: "The modifier key that pulls the HUD down while it's held on its own; off for none."}))
	x.Add(h.Option(registry.OptionSpec{Name: HoldDelayOption, Type: registry.Int, Min: 0, Max: 2000, Step: 50, Default: 250, Scopes: registry.ScopeUser,
		Help: "How long, in milliseconds, the hold key is held on its own before the HUD comes down, so a chord (⌥O) doesn't flash it."}))
	for _, c := range []registry.Command{
		{ID: "hud.toggle", Title: "Pin the HUD down, or put it away", Run: func(registry.Ctx, registry.Args) error {
			x.pinned, x.held = !x.pinned, false
			return nil
		}},
		{ID: "hud.close", Title: "Put the HUD away", Run: func(registry.Ctx, registry.Args) error {
			x.pinned, x.held = false, false
			return nil
		}},
		{ID: "hud.refresh", Title: "Build an indicator again now: {name}, or every one", Run: func(_ registry.Ctx, a registry.Args) error {
			x.refresh(a.String("name"))
			return nil
		}},
	} {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: HUDKeyMode, Keys: "<Esc>", Command: "hud.close", Desc: "Put the HUD away"}))
	x.Add(h.View(HUDView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "hud", Fn: x.drawer} })))
	x.Add(h.Contribute(uireg.Indicator{Name: "hud", Slot: uireg.SlotTitleBar, Global: true, Order: 90, Help: kit.HUDHelp,
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "hud", Fn: x.button}, nil }}))
	x.Add(kit.OnDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		name := core.EventName(ev)
		hit := false
		for _, c := range h.Contributions("hud.indicator") {
			if ind := c.(uireg.Indicator); slices.Contains(ind.On, name) {
				x.mu.Lock()
				if b := x.cache[ind.Name]; b != nil {
					b.dirty, hit = true, true
				}
				x.mu.Unlock()
			}
		}
		if hit {
			h.Redraw()
		}
	})))
	return nil
}

// refresh has name built again before it's next drawn; every indicator when name is "".
func (x *HUD) refresh(name string) {
	x.mu.Lock()
	for n, b := range x.cache {
		if name == "" || n == name {
			b.dirty = true
		}
	}
	x.mu.Unlock()
	x.h.Redraw()
}

// shown says whether the HUD is down.
func (x *HUD) shown() bool { return x.held || x.pinned }

// node is ind's node as last built, built again first when it's due: never built, refreshed, one of its events
// happened, Every passed, or the contributions changed.
func (x *HUD) node(c *ui.Ctx, ind uireg.Indicator) (ui.Node, error) {
	now := c.Now()
	gen := x.h.ContributionsGen()
	x.mu.Lock()
	if gen != x.gen {
		x.gen, x.cache = gen, map[string]*builtIndicator{}
	}
	b := x.cache[ind.Name]
	due := b == nil || b.dirty || ind.Every > 0 && !now.Before(b.at.Add(ind.Every))
	x.mu.Unlock()
	if due {
		var n ui.Node
		err := registry.Call(func() (err error) { n, err = ind.Build(context.Background()); return })
		x.h.Report("hud.indicator", ind.Name, err)
		b = &builtIndicator{node: n, err: err, at: now}
		x.mu.Lock()
		x.cache[ind.Name] = b
		x.mu.Unlock()
	}
	if ind.Every > 0 {
		c.WakeAt(b.at.Add(ind.Every))
	}
	return b.node, b.err
}

// off says whether ind has been turned off for failing.
func (x *HUD) off(ind uireg.Indicator) bool {
	for _, o := range x.h.TurnedOff() {
		if o.Kind == "hud.indicator" && o.ID == ind.Name {
			return true
		}
	}
	return false
}

// Node is ind's node to draw, or its error in its place; nil once it's been turned off.
func (x *HUD) Node(c *ui.Ctx, ind uireg.Indicator) ui.Node {
	if x.off(ind) {
		return nil
	}
	n, err := x.node(c, ind)
	if err != nil {
		th := c.Theme()
		return &ui.Func{Key: "err", Fn: func(c *ui.Ctx) {
			c.Text("err", "✕ "+ind.Name+": "+kit.FirstLine(err.Error()), kit.MonoFont(10, th.C(theme.Error)))
		}}
	}
	return n
}

// holdKey is the modifier that pulls the HUD down, or 0 for none.
func (x *HUD) holdKey() render.Mods {
	v, _, _ := x.h.Options().Get(HoldOption, registry.At{})
	s, _ := v.(string)
	return holdMods[s]
}

func (x *HUD) holdDelay() time.Duration {
	v, _, _ := x.h.Options().Get(HoldDelayOption, registry.At{})
	ms, _ := v.(int)
	return time.Duration(ms) * time.Millisecond
}

// drawer is the HUD while it's down: the hold key held alone for the delay brings it, letting go puts it away
// unless it was pinned.
func (x *HUD) drawer(c *ui.Ctx) {
	key := x.holdKey()
	if key != 0 && !x.held && !x.pinned {
		if m, since := c.HeldAlone(); m == key {
			if due := since.Add(x.holdDelay()); c.Now().Before(due) {
				c.WakeAt(due)
			} else {
				x.held = true
			}
		}
	}
	if x.held && (key == 0 || c.Held()&key == 0) {
		x.held = false
	}
	if !x.shown() {
		return
	}
	grab := cmp.Or(modGlyphs[key], "⌥") + " RELEASE TO HIDE · CLICK TO PIN"
	if x.pinned {
		grab = "PINNED · ESC OR CLICK TO PUT AWAY"
	}
	res := widget.Drawer(c, "hud", widget.DrawerOpts{Top: kit.TitleH, Pinned: x.pinned, Grab: grab, Label: "HUD", Help: kit.HUDHelp, KeyMode: HUDKeyMode},
		x.panels)
	switch {
	case res.Grabbed && x.pinned:
		x.pinned = false
	case res.Grabbed || res.Pressed && x.held:
		x.pinned, x.held = true, false
	}
}

// panels are the indicators in the hud slot, in rows of four columns.
func (x *HUD) panels(c *ui.Ctx) {
	var rows [][]uireg.Indicator
	used := hudColumns
	for _, ind := range uireg.Indicators(x.h, uireg.SlotHUD) {
		if x.off(ind) {
			continue
		}
		span := min(max(ind.Span, 1), hudColumns)
		if used+span > hudColumns {
			rows, used = append(rows, nil), 0
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], ind)
		used += span
	}
	for i, row := range rows {
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			taken := 0
			for _, ind := range row {
				span := min(max(ind.Span, 1), hudColumns)
				taken += span
				note := ""
				if ind.Note != nil {
					registry.Call(func() error { note = ind.Note(); return nil })
				}
				n := x.Node(c, ind)
				widget.Panel(c, ind.Name, widget.PanelOpts{Title: cmp.Or(ind.Title, ind.Name), Note: note, Help: cmp.Or(ind.Help, kit.HUDHelp+"#panels"),
					W: ui.Flex(float32(span))}, func(c *ui.Ctx) {
					if n != nil {
						c.Node(n)
					}
				})
			}
			if taken < hudColumns {
				c.Box("rest", ui.Style{W: ui.Flex(float32(hudColumns - taken))}, nil)
			}
		})
	}
}

// button is the title bar's ⌥ HUD: lit while the HUD is down; a click pins it or puts it away.
func (x *HUD) button(c *ui.Ctx) {
	th := c.Theme()
	fg, line := th.C(tHUDKey), th.C(theme.LineStrong)
	if x.shown() {
		fg, line = th.C(tHUDKeyOn), theme.WithAlpha(th.C(tHUDKeyOn), .6)
	}
	if widget.Clickable(c, "hud", ui.Style{Pad: ui.PadXY(7, 3), Radius: 4, Stroke: line, Border: 1, Label: "HUD", Help: kit.HUDHelp,
		Tooltip: "Hold ⌥ for the HUD; click to pin it"}, func(c *ui.Ctx) {
		c.Text("k", "⌥ HUD", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .06}, Color: fg})
	}).Clicked {
		x.h.Run(context.Background(), "hud.toggle", nil)
	}
}
