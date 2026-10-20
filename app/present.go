package app

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// PresentKeyMode is a presentation's keys: j/k between blocks, c or ⏎ to comment on one, o to go to its source.
const PresentKeyMode = "present"

var tPresentSel = theme.DeclareOn("present.block.selected", "alpha(accent, 6%)", "bg.base")

// presentState is a presentation's place: the selected block, the scroll, and what to do next frame.
type presentState struct {
	sel    int // the selected block, or -1
	scroll scrollState
	gaps   map[string]float32 // the space above each margin card, as last laid out
	focus  bool               // take focus next frame
	reveal bool               // bring the selected block into view next frame
	top    int                // a source line to bring to the top, -1 for none
	shown  int                // the source line at the top, as last drawn
	at     float32            // the scroll, as last drawn
	settle int                // frames to wait before a change in the scroll is the user's
	frames int                // how many it's been drawn
	blocks []*registry.PresentBlock
	built  presentBuild
	height []float32     // each block's box, as last laid out; 0 before it's been
	drawn  map[int]ui.ID // the blocks built this frame, by their boxes
	laid   map[int]ui.ID // and last frame
}

// blockGuess is a block's height before it's been laid out.
const blockGuess = 40

// place is where each of n blocks' boxes starts in the content, and where the last ends: each as tall as it was last
// laid out, or blockGuess before it has been.
func (st *presentState) place(c *ui.Ctx, n int) []float32 {
	st.drawn, st.laid = st.laid, st.drawn
	clear(st.drawn)
	for i, id := range st.laid {
		if r, ok := c.Rect(id); ok && i < len(st.height) {
			st.height[i] = r.Dy()
		}
	}
	st.height = slices.Grow(st.height[:min(len(st.height), n)], n)[:n]
	tops := make([]float32, n+1)
	tops[0] = 28 // the doc's top padding
	for i, h := range st.height {
		if h <= 0 {
			h = blockGuess
		}
		tops[i+1] = tops[i] + h
	}
	return tops
}

// presentBuild is what a presenter built from a buffer, kept until the text, the width or the contributions change.
type presentBuild struct {
	buf       *text.Buffer
	version   uint64
	path      string
	presenter string
	width     float32
	gen       int
	ctx       *registry.PresentCtx
	at        []int // each line's block
	err       error
}

// build is the presentation's blocks, built again only when what they're built from has changed.
func (st *presentState) build(h plugin.Host, p presentation, snap text.Snapshot, width float32) *presentBuild {
	b, gen := &st.built, h.ContributionsGen()
	if b.ctx != nil && b.buf == p.buf && b.version == snap.Version() && b.path == p.path && b.presenter == p.presenter.Name && b.width == width &&
		b.gen == gen {
		return b
	}
	ctx := &registry.PresentCtx{Path: p.path, Name: p.name, Lines: strings.Split(strings.TrimSuffix(snap.String(), "\n"), "\n"), Width: width}
	err := registry.Call(func() error { p.presenter.Build(ctx); return nil })
	if err != nil {
		h.Report(registry.KindView, "presenter."+p.presenter.Name, err)
	}
	*b = presentBuild{buf: p.buf, version: snap.Version(), path: p.path, presenter: p.presenter.Name, width: width, gen: gen, ctx: ctx,
		at: ctx.LineBlocks(snap.Lines()), err: err}
	return b
}

func newPresentState() *presentState {
	return &presentState{sel: -1, top: -1, gaps: map[string]float32{}, drawn: map[int]ui.ID{}, laid: map[int]ui.ID{}}
}

// presentation is a presenter drawing a buffer: in an editor window, or a view of its own (plan mode).
type presentation struct {
	presenter  registry.BufferPresenter
	path, name string
	buf        *text.Buffer
	window     string // the DecorationCtx's
	kicker     string // the document's label, for a presenter that draws a title
	focused    bool
	root       ui.ID     // focus anywhere in it is the presentation's
	source     func(int) // go to a 0-based source line
}

// presentFocus is the presentation that has focus, for its keys' commands.
type presentFocus struct {
	st     *presentState
	p      presentation
	h      plugin.Host
	opened bool // a thread was opened on a block this frame
}

// presentCommands are the keys of whatever presentation has focus.
func presentCommands(sh *shell, h plugin.Host) []registry.Command {
	on := func(fn func(f *presentFocus)) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error {
			if f := sh.present; f != nil {
				fn(f)
			}
			return nil
		}
	}
	has := func(registry.Args) bool { return sh.present != nil }
	return []registry.Command{
		{ID: "present.next", Title: "Presentation: the next block", Available: has, Run: on(func(f *presentFocus) { f.move(1) })},
		{ID: "present.prev", Title: "Presentation: the block before", Available: has, Run: on(func(f *presentFocus) { f.move(-1) })},
		{ID: "present.comment", Title: "Presentation: open a thread on the selected block's lines", Available: has,
			Run: on(func(f *presentFocus) { f.comment(max(f.st.sel, 0)) })},
		{ID: "present.source", Title: "Presentation: go to the selected block's source", Available: has, Run: on(func(f *presentFocus) {
			if f.p.source == nil || len(f.st.blocks) == 0 {
				return
			}
			line := f.st.shown
			if f.st.sel >= 0 && f.st.sel < len(f.st.blocks) {
				line = f.st.blocks[f.st.sel].From
			}
			f.p.source(line)
		})},
	}
}

func presentKeys() []registry.Keymap {
	var out []registry.Keymap
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "present.next", Desc: "Next block"},
		{Keys: "<Down>", Command: "present.next", Desc: "Next block"},
		{Keys: "k", Command: "present.prev", Desc: "Block before"},
		{Keys: "<Up>", Command: "present.prev", Desc: "Block before"},
		{Keys: "c", Command: "present.comment", Desc: "Comment on it"},
		{Keys: "<CR>", Command: "present.comment", Desc: "Comment on it"},
		{Keys: "o", Command: "present.source", Desc: "Go to its source"},
	} {
		k.Mode = PresentKeyMode
		out = append(out, k)
	}
	return out
}

func (f *presentFocus) move(d int) {
	if len(f.st.blocks) == 0 {
		return
	}
	f.st.sel = min(max(f.st.sel+d, 0), len(f.st.blocks)-1)
	f.st.reveal = true
}

// comment opens a thread on block i's lines, when the presenter takes comments.
func (f *presentFocus) comment(i int) {
	if !f.p.presenter.Comments || i >= len(f.st.blocks) {
		return
	}
	f.st.sel = i
	b := f.st.blocks[i]
	a := registry.Args{"file": f.p.path, "line": b.From + 1, "window": f.p.window}
	if b.To > b.From {
		a["end_line"] = b.To + 1
	}
	f.opened = true
	if err := f.h.Run(context.Background(), "thread.new", a); err != nil {
		f.h.Notify(core.Notice{ID: "present", Severity: core.SevWarning, Source: "editor", Key: true, Text: err.Error()})
	}
}

// blockDecor is what a presentation's decorations put on one block.
type blockDecor struct {
	all   []registry.Decoration
	cards []registry.Decoration // its Blocks, drawn in the margin
	signs []registry.Decoration
	bg    *registry.Decoration // a LineBg: its fill and edge
	label *registry.Decoration // a VirtualText, as a label at its top right
	mark  string               // an Underline or Highlight's style, for its edge
}

// blockDecorations are the decorations of a presentation's buffer, put on the blocks holding their lines.
func blockDecorations(h plugin.Host, dctx registry.DecorationCtx, b *presentBuild) []blockDecor {
	decor := make([]blockDecor, len(b.ctx.Blocks()))
	ds := h.Decorate(dctx)
	for _, line := range slices.Sorted(maps.Keys(ds)) {
		i := -1
		if line < len(b.at) {
			i = b.at[line]
		}
		if i < 0 {
			continue
		}
		bd := &decor[i]
		for _, d := range ds[line] {
			d.Line = line
			bd.all = append(bd.all, d)
			switch d.Kind {
			case registry.Block:
				if d.Node != nil {
					bd.cards = append(bd.cards, d)
				}
			case registry.Sign:
				bd.signs = append(bd.signs, d)
			case registry.LineBg:
				if bd.bg == nil {
					bd.bg = &d
				}
			case registry.VirtualText:
				if bd.label == nil {
					bd.label = &d
				}
			case registry.Underline, registry.Highlight:
				if bd.mark == "" {
					bd.mark = d.Style
				}
			}
		}
	}
	return decor
}

// drawPresentation draws a presentation: its blocks, numbered where something is, the selected one lit, and the
// margin of cards (the file's threads) level with their blocks. It reports the source line at its top, and
// whether the reader scrolled it there.
func drawPresentation(c *ui.Ctx, h plugin.Host, sh *shell, st *presentState, p presentation, width float32) (top int, scrolled bool) {
	th := c.Theme()
	snap := p.buf.Snapshot()
	body := c.Next("body")
	if r, ok := c.Rect(body); ok {
		width = r.Dx()
	}
	margin := min(max(width*.38, 300), 520)
	docW := min(max(width-margin-120, 280), 760)
	built := st.build(h, p, snap, docW)
	if built.err != nil {
		c.Text("error", p.presenter.Name+": "+built.err.Error(), ui.TextStyle{Color: th.C(theme.Error)})
		return st.shown, false
	}
	ctx := built.ctx
	blocks := ctx.Blocks()
	st.blocks = blocks
	st.sel = min(st.sel, len(blocks)-1)
	dctx := registry.DecorationCtx{Window: p.window, Presenter: p.presenter.Name, Now: c.Now(), Name: p.name, Path: p.path,
		Buffer: p.buf, Snap: snap, Top: 0, Bottom: snap.Lines(), FileBytes: snap.Bytes, Wake: c.WakeAt}
	decor := blockDecorations(h, dctx, built)
	focus := &presentFocus{st: st, p: p, h: h}
	asked := false
	if p.focused && !c.FocusWithin(p.root) || st.focus {
		c.Focus(body)
		st.focus, asked = false, true
	}
	if asked || c.FocusWithin(p.root) {
		sh.present = focus
	} else if f := sh.present; f != nil && f.st == st {
		sh.present = nil
	}
	ids := make([]ui.ID, len(blocks))
	var cards []ui.ID
	view, okV := c.Rect(body)
	content, okC := c.Rect(ui.Child(body, "content"))
	okV, okC = okV && st.frames > 0, okC && st.frames > 0 // laid out once at least: not what was here before
	tops := st.place(c, len(blocks))
	// where block i's clickable top and bottom were last laid out; only the blocks near the view are built
	span := func(i int) (float32, float32, bool) {
		if _, laid := st.laid[i]; laid && ids[i] != 0 {
			r, _ := c.Rect(ids[i])
			return r.Min.Y, r.Max.Y, true
		}
		y := content.Min.Y + tops[i]
		return y + blocks[i].Space, y + tops[i+1] - tops[i], okC
	}
	near := func(i int) bool {
		if !okV || !okC || i == st.sel {
			return true
		}
		at := max(min(c.Scroll(body).Y, tops[len(blocks)]-view.Dy()), 0)
		return tops[i+1] >= at-view.Dy() && tops[i] <= at+2*view.Dy()
	}
	scrolledBox(c, "body", &st.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: PresentKeyMode, Role: ui.RoleList, Label: p.name,
		Help: "editor/pane#present"}, func(c *ui.Ctx) {
		c.Row("page", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("doc", ui.Style{W: ui.Px(docW + 76), Pad: ui.Insets{L: 16, T: 28, B: 48}}, func(c *ui.Ctx) {
				gap := float32(0)
				for i, b := range blocks {
					if !near(i) {
						gap += tops[i+1] - tops[i]
						continue
					}
					if gap > 0 {
						c.Box(fmt.Sprint("gap", i), ui.Style{H: ui.Px(gap)}, nil)
						gap = 0
					}
					st.drawn[i] = c.Next(fmt.Sprint("b", i))
					ids[i] = presentBlock(c, h, focus, i, b, decor[i], docW, p.kicker)
				}
				if gap > 0 {
					c.Box("gap", ui.Style{H: ui.Px(gap)}, nil)
				}
			})
			c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			if focus.opened {
				decor = blockDecorations(h, dctx, built) // with the thread a click just opened, so it takes the keys typed next
			}
			cards = presentMargin(c, st, p, decor, span, margin)
		})
	})
	st.frames++
	switch {
	case st.top >= 0 && okV && okC:
		if i := ctx.BlockAt(st.top); i >= 0 {
			if top, _, ok := span(i); ok {
				st.at = 0
				if i > 0 {
					st.at = max(top-content.Min.Y-28, 0) // as far below the top as the first block is
				}
				c.ScrollTo(body, render.Point{Y: st.at})
				st.top, st.settle = -1, 2
			}
		}
	case st.reveal:
		if st.sel >= 0 && st.sel < len(ids) {
			if _, laid := st.laid[st.sel]; !laid {
				c.WakeAt(c.Now()) // built this frame for the first time: shown once it's laid out
				break
			}
			c.ScrollIntoView(body, ids[st.sel])
		}
		st.reveal = false
	}
	for _, id := range cards {
		c.ScrollIntoView(body, id)
	}
	st.shown = p.buf.Snapshot().Lines() - 1
	if okV {
		for i := range blocks {
			if _, bottom, ok := span(i); ok && bottom > view.Min.Y+1 {
				st.shown = blocks[i].From
				break
			}
		}
	}
	at := c.Scroll(body).Y
	if st.settle > 0 {
		st.settle--
		st.at = at
		return st.shown, false
	}
	moved := at != st.at
	st.at = at
	return st.shown, moved
}

// scrolledBox is a column that scrolls, its content keyed "content".
func scrolledBox(c *ui.Ctx, key any, st *scrollState, s ui.Style, body func(c *ui.Ctx)) {
	scrolled(c, key, st, s, ui.Style{}, func(c *ui.Ctx, _ func(string) ui.ID) { body(c) })
}

// presentBlock is one block: its number where a card is (or where it's selected), an edge in its card's or
// decoration's colour, its body, and a label at its top right. A click selects it and comments on it. It returns
// the ID of what's clicked, whose top a card for it is level with.
func presentBlock(c *ui.Ctx, h plugin.Host, f *presentFocus, i int, b *registry.PresentBlock, bd blockDecor, width float32, kicker string) ui.ID {
	th := c.Theme()
	st := f.st
	line, tone := 0, render.Color{}
	for _, d := range bd.cards {
		if line == 0 {
			line = d.Line + 1
		}
		if d.Style != "" && tone == (render.Color{}) {
			tone = tokenColour(th, d.Style, tone)
		}
	}
	if tone == (render.Color{}) && bd.mark != "" {
		tone = tokenColour(th, bd.mark, tone)
	}
	sel := st.sel == i
	fill := render.Color{}
	switch {
	case bd.bg != nil:
		fill, tone = tokenColour(th, bd.bg.Style, fill), tokenColour(th, bd.bg.Edge, tone)
	case sel:
		fill = th.C(tPresentSel)
	}
	if sel && line == 0 {
		line = b.From + 1
	}
	var id ui.ID
	c.Box(fmt.Sprint("b", i), ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: b.Space}}, func(c *ui.Ctx) {
		id = c.Next("click")
		if widget.Clickable(c, "click", ui.Style{Dir: ui.Row, W: ui.Flex(1), Fill: fill, Selected: sel, Role: ui.RoleListItem, Label: b.Label,
			Help: cmpOr(f.p.presenter.Help, "editor/pane#present")}, func(c *ui.Ctx) {
			c.Box("n", ui.Style{W: ui.Px(40), Pad: ui.Insets{R: 10, T: 4}, Main: ui.End, Dir: ui.Row}, func(c *ui.Ctx) {
				if len(bd.signs) > 0 {
					c.Row("signs", ui.Style{Gap: 2, Cross: ui.Center, Float: &ui.Float{Offset: render.Point{X: 2, Y: 3}}}, func(c *ui.Ctx) {
						for k, d := range bd.signs {
							drawSign(c, h, k, d, 13)
						}
					})
				}
				if line > 0 {
					c.Text("t", fmt.Sprint(line), monoFont(10.5, th.C(theme.FgGhost)))
				}
			})
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: tone}, nil)
			pad := ui.Insets{L: 12, R: 12, T: 2, B: 2}
			if bd.label != nil {
				pad.T, pad.B = 14, 8 // room for its label
			}
			c.Box("in", ui.Style{W: ui.Px(width + 24), Pad: pad}, func(c *ui.Ctx) {
				if b.Body != nil {
					b.Body(c, registry.PresentDraw{Width: width, Decorations: bd.all, Kicker: kicker})
				}
			})
		}).Clicked {
			st.sel = i
			f.comment(i)
		}
		if l := bd.label; l != nil {
			edge := tokenColour(th, l.Style, th.C(theme.FgFaint))
			c.Box("label", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: 0}, Self: render.Point{X: 1, Y: .5}, Offset: render.Point{X: -8, Y: b.Space}}},
				func(c *ui.Ctx) {
					c.Row("chip", ui.Style{Pad: ui.PadXY(8, 3), Gap: 6, Cross: ui.Center, Radius: 10, Fill: th.C(theme.BgBase), Stroke: theme.WithAlpha(edge, .5),
						Border: 1, Role: ui.RoleText, Label: l.Text}, func(c *ui.Ctx) {
						c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: edge}, nil)
						c.Text("t", strings.ToUpper(l.Text), kickerFont(edge))
					})
				})
		}
	})
	return id
}

// presentMargin is the blocks' cards, each level with its block (or under the card before it), where span says it
// was laid out. It returns the cards that want revealing.
func presentMargin(c *ui.Ctx, st *presentState, p presentation, decor []blockDecor, span func(int) (float32, float32, bool), width float32) (reveal []ui.ID) {
	th := c.Theme()
	type entry struct {
		key   string
		block int
		d     registry.Decoration
	}
	var entries []entry
	for i, bd := range decor {
		for k, d := range bd.cards {
			key := fmt.Sprint("d", d.Line, ".", k)
			if f, ok := d.Node.(*ui.Func); ok && f.Key != nil {
				key = fmt.Sprint(f.Key)
			}
			entries = append(entries, entry{key: key, block: i, d: d})
		}
	}
	id := c.Next("margin")
	c.Column("margin", ui.Style{W: ui.Px(width), Pad: ui.Insets{L: 20, R: 20, T: 28, B: 48}}, func(c *ui.Ctx) {
		col, ok := c.Rect(id)
		y := col.Min.Y + 28
		for _, e := range entries {
			gap := float32(0)
			if top, _, okB := span(e.block); ok && okB {
				gap = max(top-y, 0)
			}
			gap = float32(int(gap))
			if old, seen := st.gaps[e.key]; !seen || old != gap {
				st.gaps[e.key] = gap
				c.WakeAt(c.Now()) // laid out against the blocks' places next frame
			}
			c.Box("gap."+e.key, ui.Style{H: ui.Px(gap)}, nil)
			card := c.Next(e.key)
			c.Box(e.key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 10}}, func(c *ui.Ctx) {
				if f, ok := e.d.Node.(*ui.Func); ok {
					f.Fn(c)
					return
				}
				c.Node(e.d.Node)
			})
			if e.d.Reveal {
				reveal = append(reveal, card)
			}
			if r, okC := c.Rect(card); okC {
				y += gap + r.Dy()
			} else {
				y += gap
			}
		}
		if len(entries) == 0 && p.presenter.Comments {
			c.Text("none", "Click a line to comment on it.", uiFont(12, th.C(theme.FgGhost)))
		}
	})
	return reveal
}
