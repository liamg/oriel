package app

import (
	"context"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

const navHelp = "shell/navigate"

// navStop is somewhere the window's been: the session in front ("" for none) and the place showing ("" for none).
type navStop struct {
	session core.SessionID
	place   string
}

// navHistory is the window's stops, back and forward, like a browser's. It's the shell's, so it outlives the plugins
// starting again on another session's directory.
type navHistory struct {
	stops []navStop
	at    int
	// going is the stop a move is putting up, until the window gets there or settles elsewhere by until.
	going *navStop
	until time.Time
}

const (
	navDepth  = 100
	navSettle = 3 * time.Second // a move to a session in another directory starts the plugins again first
)

// see records where the window is now. While a move settles, where it lands takes that stop's place.
func (n *navHistory) see(s navStop, now time.Time) {
	if len(n.stops) == 0 {
		n.stops, n.at = []navStop{s}, 0
		return
	}
	if n.going != nil {
		if s == *n.going || now.After(n.until) {
			n.going = nil
		}
		n.stops[n.at] = s
		return
	}
	if n.stops[n.at] == s {
		return
	}
	n.stops = append(n.stops[:n.at+1], s)
	if len(n.stops) > navDepth {
		n.stops = n.stops[len(n.stops)-navDepth:]
	}
	n.at = len(n.stops) - 1
}

// step moves dir stops along, past those ok turns down, and says where to.
func (n *navHistory) step(dir int, ok func(navStop) bool, now time.Time) (navStop, bool) {
	for i := n.at + dir; i >= 0 && i < len(n.stops); i += dir {
		if s := n.stops[i]; ok(s) {
			n.at, n.going, n.until = i, &s, now.Add(navSettle)
			return s, true
		}
	}
	return navStop{}, false
}

// seeNav records where the window is, before each frame.
func (sh *shell) seeNav(r registry.Registry, now time.Time) {
	if place, ok := registry.ShownPlace(r); ok {
		sh.nav.see(navStop{session: sh.cfg.Store.Focus(), place: place}, now)
	}
}

// navPlugin is back and forward through the window's history: nav.back and nav.forward, on the mouse's side buttons
// wherever the focus has no back of its own. Pages join the history as places (registry.Place).
type navPlugin struct {
	disposer
	sh *shell
	h  plugin.Host
}

func (x *navPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "nav"} }

func (x *navPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	for _, c := range []registry.Command{
		{ID: "nav.back", Title: "Back to the session or page before", Run: func(ctx registry.Ctx, _ registry.Args) error { return x.move(ctx, -1) }},
		{ID: "nav.forward", Title: "Forward to the session or page after", Run: func(ctx registry.Ctx, _ registry.Args) error { return x.move(ctx, 1) }},
	} {
		x.add(h.Command(c))
	}
	for _, mode := range []string{"global", ViewKeyMode, EmptyKeyMode} {
		x.add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<X1Mouse>", Command: "nav.back", Desc: "Back"}))
		x.add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<X2Mouse>", Command: "nav.forward", Desc: "Forward"}))
	}
	return nil
}

func (x *navPlugin) move(ctx context.Context, dir int) error {
	now := x.h.UI().Now()
	x.sh.seeNav(x.h, now)
	if len(x.sh.nav.stops) == 0 {
		return nil
	}
	from := x.sh.nav.stops[x.sh.nav.at]
	to, ok := x.sh.nav.step(dir, x.reachable, now)
	if !ok {
		return nil
	}
	var then []call
	if to.place != "" && to.place != from.place {
		p, _ := registry.PlaceByName(x.h, to.place)
		then = []call{{command: p.Command, args: p.Args}}
	}
	if to.place == "" && from.place != "" {
		if p, ok := registry.PlaceByName(x.h, from.place); ok && p.Close != nil {
			registry.Call(func() error { p.Close(); return nil })
		}
	}
	switch {
	case to.session == from.session:
		runCalls(x.h, then)
	case to.session == "":
		if err := x.sh.cfg.Store.Apply(core.SessionFocused{}); err != nil {
			return err
		}
		runCalls(x.h, then)
	default:
		var calls []any
		for _, c := range then {
			calls = append(calls, registry.Args{"command": c.command, "args": c.args})
		}
		return x.h.Run(ctx, "session.focus", registry.Args{"id": string(to.session), "then": calls})
	}
	return nil
}

// reachable says whether the window can go back to s: its session is still open, and its place still there.
func (x *navPlugin) reachable(s navStop) bool {
	if s.session != "" {
		se, ok := x.sh.cfg.Store.Session(s.session)
		if !ok || se.Snapshot().Archived {
			return false
		}
	}
	if s.place != "" {
		if _, ok := registry.PlaceByName(x.h, s.place); !ok {
			return false
		}
	}
	return true
}

// pagePlace is a page shown while *open, as a place in the window's history.
func pagePlace(name, title, command string, open *bool) registry.Place {
	return registry.Place{Name: name, Title: title, Command: command, Shown: func() bool { return *open }, Close: func() { *open = false }}
}
