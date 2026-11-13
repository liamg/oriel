package shell

import (
	"context"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
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
	if place, ok := uireg.ShownPlace(r); ok {
		sh.nav.see(navStop{session: sh.store.Focus(), place: place}, now)
	}
}

// navPlugin is back and forward through the window's history: nav.back and nav.forward, on the mouse's side buttons
// wherever the focus has no back of its own. Pages join the history as places (uireg.Place).
type navPlugin struct {
	kit.Disposer
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
		x.Add(h.Command(c))
	}
	for _, mode := range []string{"global", ViewKeyMode, EmptyKeyMode} {
		x.Add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<X1Mouse>", Command: "nav.back", Desc: "Back"}))
		x.Add(h.Keymap(registry.Keymap{Mode: mode, Keys: "<X2Mouse>", Command: "nav.forward", Desc: "Forward"}))
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
	var then []kit.Call
	if to.place != "" && to.place != from.place {
		p, _ := uireg.PlaceByName(x.h, to.place)
		then = []kit.Call{{Command: p.Command, Args: p.Args}}
	}
	if to.place == "" && from.place != "" {
		if p, ok := uireg.PlaceByName(x.h, from.place); ok && p.Close != nil {
			registry.Call(func() error { p.Close(); return nil })
		}
	}
	switch {
	case to.session == from.session:
		kit.RunCalls(x.h, then)
	case to.session == "":
		if err := x.h.Apply(core.SessionFocused{}); err != nil {
			return err
		}
		kit.RunCalls(x.h, then)
	default:
		return x.h.Run(ctx, "session.focus", registry.Args{"id": string(to.session), "then": kit.Then(then...)})
	}
	return nil
}

// reachable says whether the window can go back to s: its session is still open, and its place still there; one with
// no session, while tabs are open, isn't.
func (x *navPlugin) reachable(s navStop) bool {
	if s.session == "" && len(x.h.State().Tabs) > 0 {
		return false // the start view: there's none with tabs open
	}
	if s.session != "" {
		se, ok := x.h.Session(s.session)
		if !ok || se.Snapshot().Archived {
			return false
		}
	}
	if s.place != "" {
		if _, ok := uireg.PlaceByName(x.h, s.place); !ok {
			return false
		}
	}
	return true
}

// placeShown says a place (a page) is showing over the layout.
func placeShown(r registry.Registry) bool {
	name, ok := uireg.ShownPlace(r)
	return name != "" || !ok
}

// closePlaces puts away every place showing, as something else is put in front.
func closePlaces(r registry.Registry) {
	for _, c := range r.Contributions("place") {
		p := c.(uireg.Place)
		if p.Shown == nil || p.Close == nil {
			continue
		}
		shown := false
		registry.Call(func() error { shown = p.Shown(); return nil })
		if shown {
			registry.Call(func() error { p.Close(); return nil })
		}
	}
}
