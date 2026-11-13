package uireg

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
)

// DecorationKind is what a decoration draws.
type DecorationKind int

const (
	Sign        DecorationKind = iota // a mark in the gutter: Text in Style's colour
	LineBg                            // the line's background, and an edge in the gutter: Style
	Highlight                         // columns [From, To) of the line: Style as their colour or background
	Underline                         // a wavy underline under [From, To): Style
	VirtualText                       // Text after the end of the line, in Style
	Block                             // Node, drawn between this line and the next, as wide as the text
	Inlay                             // Text drawn inline before byte column From, in Style: an inlay hint
)

// Decoration is something drawn on, beside or between a buffer's lines. It's on Line, or at Anchor when that's
// set: an anchor the provider made on the buffer, which moves with edits, so a block stays with its text.
type Decoration struct {
	Kind     DecorationKind
	Line     int
	Anchor   text.AnchorID
	From, To int    // byte columns, for Highlight and Underline; From for Inlay
	Style    string // a theme token: "diff.add", "error", or a syntax group "@comment"
	Edge     string // a LineBg's gutter edge, a theme token
	Text     string
	Node     ui.Node // a Block's content
	Reveal   bool    // a Block: the editor scrolls so all of it shows, as a references peek or a new thread wants
	Priority int     // higher draws over lower
	// Command, for a Sign, runs with Args when the sign is clicked: a test's ▶, a breakpoint.
	Command string
	Args    registry.Args
	// Modified are the commands a Sign's click runs instead with a modifier held, by its name ("shift", "alt",
	// "ctrl", "super"), with Args: ⇧ on a test's ▶ debugs it.
	Modified map[string]string
	Tooltip  string
	// Menu, for a Sign, is what a right-click on it offers; an item whose command isn't Available with its Args is
	// left out.
	Menu []SignItem
	// Icon draws a Sign as a built-in icon ("comment"), Text beside it small: a count.
	Icon string
	// Hover, for a Sign, is a card shown while the pointer is on the sign or the card; it never takes focus.
	Hover ui.Node
	// OnHover draws a Sign only while the pointer is on its line: a line's + to comment on it. On a line with
	// another sign it's an item in that one's menu instead.
	OnHover bool
	// Bg, for VirtualText, is a theme token it's drawn on, as a chip: a debugger's inline values.
	Bg string
	// Was, for a LineBg on a changed line, is what the line read before, for a presenter that shows the change.
	Was string
}

// EveryLine is the Line of a Sign drawn on each line, however many the file has: one decoration, which Decorate gives
// under this line, and a gutter that draws it puts on each of its lines with OnLine. A presenter of blocks leaves it out.
const EveryLine = -1

// OnLine is an EveryLine sign as it's drawn on line (0-based): its Args with that "line" (1-based), and "{line}" in its
// Tooltip as the number.
func (d Decoration) OnLine(line int) Decoration {
	d.Line = line
	args := make(registry.Args, len(d.Args)+1)
	for k, v := range d.Args {
		args[k] = v
	}
	args["line"] = line + 1
	d.Args = args
	d.Tooltip = strings.ReplaceAll(d.Tooltip, "{line}", strconv.Itoa(line+1))
	return d
}

// SignItem is an entry in a Sign's right-click menu: Label runs Command with Args.
type SignItem struct {
	Label   string
	Command string
	Args    registry.Args
}

// TopSign is the sign drawn when a line has room for one, of signs: the one with the highest priority, the latest
// of equals, with each of the others' commands and menus added to its menu, so none is out of reach.
func TopSign(signs []Decoration) (Decoration, bool) {
	top := -1
	for i, d := range signs {
		if top < 0 || signs[top].OnHover && !d.OnHover || d.OnHover == signs[top].OnHover && d.Priority >= signs[top].Priority {
			top = i
		}
	}
	if top < 0 {
		return Decoration{}, false
	}
	d := signs[top]
	if len(signs) == 1 {
		return d, true
	}
	d.Menu = slices.Clone(d.Menu)
	for i := len(signs) - 1; i >= 0; i-- {
		o := signs[i]
		if i == top {
			continue
		}
		if o.Command != "" {
			d.Menu = append(d.Menu, SignItem{Label: cmp.Or(o.Tooltip, o.Command), Command: o.Command, Args: o.Args})
		}
		d.Menu = append(d.Menu, o.Menu...)
	}
	return d, true
}

// DecorationCtx is the buffer a provider decorates and the lines in view, [Top, Bottom), and where it's drawn: the
// editor window (its id), and the presenter drawing it, "" for the text. Blocks in a presentation are drawn in its
// margin, beside the block holding their line.
type DecorationCtx struct {
	Window      string
	Presenter   string
	Now         time.Time // the frame's
	Name        string    // the buffer's file, as the editor names it
	Path        string    // the buffer's file, absolute
	Buffer      *text.Buffer
	Snap        text.Snapshot
	Top, Bottom int
	// FileBytes is the buffer as writing it would put it on disk (line endings, BOM, the last newline), for
	// comparing with a file's other versions.
	FileBytes func() []byte
	// Wake asks for a frame at a time: Decorate calls it for a RefreshAt. Nil for none.
	Wake func(time.Time)

	refresh *time.Time
}

// RefreshAt asks for the provider's decorations again at t though nothing else changes: "edited 4s ago".
func (c DecorationCtx) RefreshAt(t time.Time) {
	if c.refresh != nil && (c.refresh.IsZero() || t.Before(*c.refresh)) {
		*c.refresh = t
	}
}

// DecorationProvider gives the decorations of the lines in view. Decorate asks again only when the buffer's text,
// the lines in view or where they're drawn change, when the provider says its decorations have
// (Decorations.DecorationsChanged), or at a time it asked for (DecorationCtx.RefreshAt).
type DecorationProvider interface {
	Decorations(ctx DecorationCtx) []Decoration
}

// DecorationKeyer is a provider that says what its decorations of ctx are made from, cheaply and comparably (a
// generation, a pointer): Decorate asks again when that changes, as it does when the buffer or the view does.
type DecorationKeyer interface {
	DecorationKey(ctx DecorationCtx) any
}

// KeyedDecorations is a DecorationKeyer from funcs: Make's decorations, made from what Key says.
type KeyedDecorations struct {
	Key  func(ctx DecorationCtx) any
	Make func(ctx DecorationCtx) []Decoration
}

func (k KeyedDecorations) Decorations(ctx DecorationCtx) []Decoration { return k.Make(ctx) }
func (k KeyedDecorations) DecorationKey(ctx DecorationCtx) any        { return k.Key(ctx) }

// DecorationFunc is a func as a DecorationProvider.
type DecorationFunc func(ctx DecorationCtx) []Decoration

func (f DecorationFunc) Decorations(ctx DecorationCtx) []Decoration { return f(ctx) }

// NamedProvider is a provider with the id it was registered under.
type NamedProvider struct {
	ID string
	DecorationProvider
}

func (NamedProvider) ContributionKind() string   { return registry.KindDecoration }
func (p NamedProvider) ContributionName() string { return p.ID }

// decorState is what Decorate keeps, shared by every WithSource of a registry.
type decorState struct {
	mu    sync.Mutex
	gen   map[string]int // by provider id: moves as it's registered, disposed or changed
	cache decorCache
}

func (r *reg) Decorations(id string, p DecorationProvider) registry.Disposable {
	d := r.Contribute(NamedProvider{id, p})
	r.DecorationsChanged(id)
	return registry.DisposeFunc(func() {
		d.Dispose()
		r.DecorationsChanged(id)
	})
}

func (r *reg) DecorationsChanged(id string) {
	r.decor.mu.Lock()
	defer r.decor.mu.Unlock()
	r.decor.gen[id]++
}

// decorSpot is where decorations are drawn: a window, its presenter, and the file in it.
type decorSpot struct{ window, presenter, path string }

// decorAsked is what a provider gave for one spot, and what it was asked with.
type decorAsked struct {
	buf      *text.Buffer
	version  uint64
	top, bot int
	gen      int
	key      any
	until    time.Time // asked again from then (RefreshAt)
	ds       []Decoration
}

// decorDrawn is one spot's decorations by line, and what they were made from.
type decorDrawn struct {
	from  []*decorAsked
	buf   *text.Buffer
	ver   uint64
	top   int
	bot   int
	lines map[int][]Decoration
	used  int // the Decorate call that last used it
}

// decorCache is what Decorate keeps between calls.
type decorCache struct {
	mu    sync.Mutex
	asked map[decorSpot]map[string]*decorAsked
	drawn map[decorSpot]*decorDrawn
	calls int
}

// decorKeep is how many calls a spot's decorations are kept unused: a window that's closed.
const decorKeep = 4096

func (r *reg) Decorate(ctx DecorationCtx) map[int][]Decoration {
	provs := r.DecorationProviders()
	r.decor.mu.Lock()
	gens := make([]int, len(provs))
	for i, p := range provs {
		gens[i] = r.decor.gen[p.ID]
	}
	r.decor.mu.Unlock()
	c := &r.decor.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.calls%decorKeep == 0 {
		for k, d := range c.drawn {
			if c.calls-d.used > decorKeep {
				delete(c.drawn, k)
				delete(c.asked, k)
			}
		}
	}
	spot := decorSpot{ctx.Window, ctx.Presenter, ctx.Path}
	if c.asked == nil {
		c.asked, c.drawn = map[decorSpot]map[string]*decorAsked{}, map[decorSpot]*decorDrawn{}
	}
	asked := c.asked[spot]
	if asked == nil {
		asked = map[string]*decorAsked{}
		c.asked[spot] = asked
	}
	version := ctx.Snap.Version()
	from := make([]*decorAsked, 0, len(provs))
	var wake time.Time
	for i, prov := range provs {
		if r.WhyOff(registry.KindDecoration, prov.ID) != nil {
			continue
		}
		var key any
		if k, ok := prov.DecorationProvider.(DecorationKeyer); ok {
			if err := registry.Call(func() error { key = k.DecorationKey(ctx); return nil }); err != nil {
				r.TurnOff(registry.KindDecoration, prov.ID, err)
				delete(asked, prov.ID)
				continue
			}
		}
		a := asked[prov.ID]
		if a == nil || a.buf != ctx.Buffer || a.version != version || a.top != ctx.Top || a.bot != ctx.Bottom || a.gen != gens[i] ||
			a.key != key || !a.until.IsZero() && !ctx.Now.Before(a.until) {
			a = &decorAsked{buf: ctx.Buffer, version: version, top: ctx.Top, bot: ctx.Bottom, gen: gens[i], key: key}
			pctx := ctx
			pctx.refresh = &a.until
			if err := registry.Call(func() error { a.ds = prov.Decorations(pctx); return nil }); err != nil {
				r.TurnOff(registry.KindDecoration, prov.ID, err)
				delete(asked, prov.ID)
				continue
			}
			asked[prov.ID] = a
		}
		if !a.until.IsZero() && (wake.IsZero() || a.until.Before(wake)) {
			wake = a.until
		}
		from = append(from, a)
	}
	if !wake.IsZero() && ctx.Wake != nil {
		ctx.Wake(wake)
	}
	d := c.drawn[spot]
	if d == nil || !slices.Equal(d.from, from) || d.buf != ctx.Buffer || d.ver != version || d.top != ctx.Top || d.bot != ctx.Bottom {
		d = &decorDrawn{from: from, buf: ctx.Buffer, ver: version, top: ctx.Top, bot: ctx.Bottom, lines: byLine(ctx, from)}
		c.drawn[spot] = d
	}
	d.used = c.calls
	return d.lines
}

// byLine is the providers' decorations of ctx's lines [Top, Bottom), anchored ones where their anchors are, each
// line's in priority order.
func byLine(ctx DecorationCtx, from []*decorAsked) map[int][]Decoration {
	out := map[int][]Decoration{}
	for _, a := range from {
		for _, d := range a.ds {
			if d.Anchor != 0 {
				o, ok := ctx.Buffer.Resolve(d.Anchor)
				if !ok {
					continue
				}
				d.Line = ctx.Snap.PosAt(min(o, ctx.Snap.Len())).Line
			}
			if d.Line >= ctx.Top && d.Line < ctx.Bottom || d.Line == EveryLine && d.Anchor == 0 {
				out[d.Line] = append(out[d.Line], d)
			}
		}
	}
	for _, ds := range out {
		slices.SortStableFunc(ds, func(a, b Decoration) int { return a.Priority - b.Priority })
	}
	return out
}

func (r *reg) DecorationProviders() []NamedProvider {
	cs := r.Contributions(registry.KindDecoration)
	out := make([]NamedProvider, len(cs))
	for i, c := range cs {
		out[i] = c.(NamedProvider)
	}
	return out
}
