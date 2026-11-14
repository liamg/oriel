package uireg

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

type View interface {
	Build(ctx ViewCtx) ui.Node // pure: state in, tree out
}

// ViewCtx is what a view is built with: the window's size and session and, for a view open in the layout, which
// instance of it this is, the project it's for, and whether it has focus.
type ViewCtx struct {
	Width, Height float32
	Session       *core.SessionState // the window's session (Window.Session); nil on a frame with none
	Global        bool               // built in a Global frame (Home): only parts that are Global show
	Instance      string
	Focused       bool
	Project       string // the project the instance was opened for (view.open's project); "" for a session's view
}

// EdgeToEdge is a view drawn right to its pane's edges, with no padding around it: an editor, a terminal.
type EdgeToEdge interface {
	EdgeToEdge()
}

// Titled is a view whose instances name themselves, for their tabs: an editor names its buffer.
type Titled interface {
	Title(instance string) string
}

// Moded is a view with a mode of its own the status line's mode chip shows while it has focus: a terminal's
// TERMINAL or TERM-NORMAL. Style is a theme token for the chip.
type Moded interface {
	ModeLabel(instance string) (label, style string)
}

// Marked is a view whose instances carry a small marker on their tabs: an editor's file's git status letter. Style
// is a theme token.
type Marked interface {
	Mark(instance string) (text, style string)
}

// Shown is a view that a slot drawing it (the diff's columns) leaves out while it has nothing for an instance, so
// the slot can give the room to the rest: the AI review outside a session.
type Shown interface {
	Shown(instance string) bool
}

// Persisted is a view whose instances come back when the app starts again on the same directory: the layout keeps
// each one's state, and Revive makes a new instance from it, which takes the old one's place.
type Persisted interface {
	Persist(instance string) (state json.RawMessage, ok bool)
	Revive(state json.RawMessage) (instance string, err error)
}

type ViewFunc func(ViewCtx) ui.Node

func (f ViewFunc) Build(ctx ViewCtx) ui.Node { return f(ctx) }

// SourcedView is a view with who registered it: a plugin's id, or "init.lua:31".
type SourcedView struct {
	View
	Source string
}

func (v SourcedView) ViewSource() string { return v.Source }
func (v SourcedView) Unwrap() View       { return v.View }

// viewEntry is a view as the registry keeps it: a contribution of kind registry.KindView.
type viewEntry struct {
	id string
	v  View
}

func (viewEntry) ContributionKind() string   { return registry.KindView }
func (e viewEntry) ContributionName() string { return e.id }

func (e viewEntry) ContributionSource() string {
	if sv, ok := e.v.(interface{ ViewSource() string }); ok {
		return sv.ViewSource()
	}
	return ""
}

func (r *reg) View(id string, v View) registry.Disposable { return r.Contribute(viewEntry{id, v}) }

// ViewByID returns the view under a guard: a panic while it builds turns it off instead of escaping.
func (r *reg) ViewByID(id string) (View, bool) {
	if kv, ok := slotView(r, id); ok && kv.View != nil {
		return guardedView{r, kv.Name, kv.View}, true
	}
	c, ok := r.Contribution(registry.KindView, id)
	if !ok {
		return nil, false
	}
	return guardedView{r, id, c.(viewEntry).v}, true
}

func (r *reg) Views() []string {
	gen := r.ContributionsGen()
	r.views.mu.Lock()
	defer r.views.mu.Unlock()
	if r.views.gen != gen {
		cs := r.Contributions(registry.KindView)
		out := make([]string, 0, len(cs))
		for _, c := range cs {
			out = append(out, c.ContributionName())
		}
		r.views.ids, r.views.gen = out, gen
	}
	return r.views.ids[:len(r.views.ids):len(r.views.ids)]
}

func (r *reg) ViewSource(id string) (string, bool) {
	c, ok := r.Contribution(registry.KindView, id)
	if !ok {
		return "", false
	}
	return c.(viewEntry).ContributionSource(), true
}

// SlotView is a view that stands in for the view Slot on sessions of Kinds: a review's own PR tab in place of the
// one that raises a PR. ViewByID(Slot) is it while the session in front is of one of Kinds.
type SlotView struct {
	Name   string // unique among kind views: "review.pr"
	Slot   string // the view id it stands in for: "pr"
	Kinds  []core.Kind
	View   View
	Source string
}

func (SlotView) ContributionKind() string   { return "view.slot" }
func (v SlotView) ContributionName() string { return v.Name }

// slotView is the view standing in for id on the session in front, if one does.
func slotView(r registry.Contributions, id string) (SlotView, bool) {
	var cands []SlotView
	for _, v := range registry.ContributionsOf[SlotView](r) {
		if v.Slot == id {
			cands = append(cands, v)
		}
	}
	if len(cands) == 0 {
		return SlotView{}, false
	}
	se, ok := registry.FrontSession(r)
	if !ok {
		return SlotView{}, false
	}
	for _, v := range cands {
		if slices.Contains(v.Kinds, se.Kind) {
			return v, true
		}
	}
	return SlotView{}, false
}

// guardedView turns its view off when it panics, building or drawing; a view that's off draws a placeholder.
type guardedView struct {
	r  registry.Faults
	id string
	v  View
}

func (g guardedView) Build(vc ViewCtx) ui.Node {
	if err := g.r.WhyOff(registry.KindView, g.id); err != nil {
		return offNode(g.id, err)
	}
	var n ui.Node
	if err := registry.Call(func() error { n = g.v.Build(vc); return nil }); err != nil {
		g.r.TurnOff(registry.KindView, g.id, err)
		return offNode(g.id, err)
	}
	f, ok := n.(*ui.Func)
	if !ok {
		return n
	}
	return &ui.Func{Key: f.Key, Fn: func(c *ui.Ctx) {
		c.Guard("guard", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Dir: ui.Column}, f.Fn, func(c *ui.Ctx, v any, stack []byte) {
			err := &registry.Panic{Value: v, Stack: stack}
			g.r.TurnOff(registry.KindView, g.id, err)
			c.Node(offNode(g.id, err))
		})
	}}
}

func (g guardedView) Unwrap() View { return g.v }

// As is v as a T, looking through the guard around it: As[EdgeToEdge](view).
func As[T any](v View) (T, bool) {
	for {
		if t, ok := v.(T); ok {
			return t, true
		}
		u, ok := v.(interface{ Unwrap() View })
		if !ok {
			var zero T
			return zero, false
		}
		v = u.Unwrap()
	}
}

func (g guardedView) Title(instance string) (title string) {
	t, ok := As[Titled](g.v)
	if !ok {
		return ""
	}
	registry.Call(func() error { title = t.Title(instance); return nil })
	return title
}

func offNode(id string, err error) ui.Node {
	return ui.TextNode(fmt.Sprintf("view %s is turned off: %v", id, err))
}
