// Package uireg is the registry's drawn half: views, decorations, pickers, live help blocks, and the contributions
// the window's shell draws (frames, the rail, indicators, the inbox, presenters, threads). Its contributions are kept
// in the registry like any other, by kind; uireg types them, and keeps what drawing them caches.
package uireg

import (
	"sync"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// Registry is the registry with what's drawn.
type Registry interface {
	registry.Registry
	Views
	Decorations
	Pickers
	Help
}

// Views are the views, by id.
type Views interface {
	View(id string, v View) registry.Disposable
	ViewByID(id string) (View, bool)     // a SlotView standing in for id on the session in front, else id
	Views() []string                     // every registered view id, sorted; shared, not to be changed
	ViewSource(id string) (string, bool) // who registered the view that wins id
}

// Decorations are what's drawn on, beside and between a buffer's lines.
type Decorations interface {
	Decorations(id string, p DecorationProvider) registry.Disposable
	DecorationProviders() []NamedProvider // the winning provider for each id, sorted by id
	// DecorationsChanged says provider id's decorations have changed, so Decorate asks it again everywhere.
	DecorationsChanged(id string)
	// Decorate is every provider's decorations of ctx's lines [Top, Bottom), by line, each line's in priority order:
	// what a provider gave last, until its inputs change (DecorationProvider). One that panics is turned off.
	Decorate(ctx DecorationCtx) map[int][]Decoration
}

// Pickers are the omnibox's and the pickers' sources.
type Pickers interface {
	Picker(s PickerSource) registry.Disposable
	Pickers() []PickerSource // the winning source for each id, by Order then id
}

// Help are the live blocks help pages show.
type Help interface {
	// HelpLive registers a "Right now" block a page shows with {{live "name"}}: whoever owns the data draws it.
	HelpLive(name string, fn func(c *ui.Ctx)) registry.Disposable
	HelpLiveByName(name string) (func(c *ui.Ctx), bool)
}

type reg struct {
	registry.Registry
	decor *decorState
	views *viewList
}

// viewList is Views, kept until a contribution comes or goes.
type viewList struct {
	mu  sync.Mutex
	gen int
	ids []string
}

// New is a registry with nothing drawn yet.
func New() Registry { return NewWith(registry.Config{}) }

// NewWith is New, with the registry's setup.
func NewWith(o registry.Config) Registry {
	return &reg{Registry: registry.NewWith(o), decor: &decorState{gen: map[string]int{}}, views: &viewList{gen: -1}}
}

// WithSource is registry.WithSource for r's views and pickers too: each registered through it without a source of
// its own says src.
func WithSource(r Registry, src string) Registry {
	return sourced{Registry: r, pure: registry.WithSource(r, src), src: src}
}

type sourced struct {
	Registry
	pure registry.Registry // r stamping what registry.WithSource stamps
	src  string
}

func (s sourced) Command(c registry.Command) registry.Disposable         { return s.pure.Command(c) }
func (s sourced) Keymap(k registry.Keymap) registry.Disposable           { return s.pure.Keymap(k) }
func (s sourced) Hook(h registry.Hook) registry.Disposable               { return s.pure.Hook(h) }
func (s sourced) Option(o registry.OptionSpec) registry.Disposable       { return s.pure.Option(o) }
func (s sourced) HelpPages(h registry.HelpSource) registry.Disposable    { return s.pure.HelpPages(h) }
func (s sourced) Contribute(c registry.Contribution) registry.Disposable { return s.pure.Contribute(c) }

func (s sourced) Picker(p PickerSource) registry.Disposable {
	if p.Source == "" {
		p.Source = s.src
	}
	return s.Registry.Picker(p)
}

func (s sourced) View(id string, v View) registry.Disposable {
	if _, ok := v.(SourcedView); !ok {
		v = SourcedView{View: v, Source: s.src}
	}
	return s.Registry.View(id, v)
}
