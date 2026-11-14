package explain

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tPresentSel   = theme.DeclareOn("present.block.selected", "alpha(accent, 6%)", "bg.base")
	tPresentHover = theme.DeclareOn("present.part.hover", "alpha(fg, 4%)", "bg.base")
)

// presentState is a presentation's place: the selected block, the scroll, and what to do next frame.
type presentState struct {
	sel    int                   // the selected block, or -1
	part   int                   // the selected block's selected part, or -1 for the whole block
	parts  map[int][]presentPart // each block's parts (PresentDraw.Part), as last drawn
	scroll kit.Scroll
	gaps   map[string]float32 // the space above each margin card, as last laid out
	focus  bool               // take focus next frame
	reveal bool               // bring the selected block into view next frame
	top    int                // a source line to bring to the top, -1 for none
	shown  int                // the source line at the top, as last drawn
	at     float32            // the scroll, as last drawn
	settle int                // frames to wait before a change in the scroll is the user's
	frames int                // how many it's been drawn
	blocks []*uireg.PresentBlock
	built  presentBuild
	height []float32     // each block's box, as last laid out; 0 before it's been
	drawn  map[int]ui.ID // the blocks built this frame, by their boxes
	laid   map[int]ui.ID // and last frame
}

// presentPart is a part of a block on its own source lines, 0-based, and the node its body drew it in.
type presentPart struct {
	from, to int
	id       ui.ID
}

// partAt is the part of block i holding a 0-based line, or -1.
func (st *presentState) partAt(i, line int) int {
	for k, p := range st.parts[i] {
		if p.from <= line && line <= p.to {
			return k
		}
	}
	return -1
}

// selected is the selected part of the selected block, if one is.
func (st *presentState) selected() (presentPart, bool) {
	if ps := st.parts[st.sel]; st.part >= 0 && st.part < len(ps) {
		return ps[st.part], true
	}
	return presentPart{}, false
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
	ctx       *uireg.PresentCtx
	at        []int // each line's block
	err       error
}

// build is the presentation's blocks, built again only when what they're built from has changed.
func (st *presentState) build(h plugin.Host, p kit.Presentation, snap text.Snapshot, width float32) *presentBuild {
	b, gen := &st.built, h.ContributionsGen()
	if b.ctx != nil && b.buf == p.Buf && b.version == snap.Version() && b.path == p.Path && b.presenter == p.Presenter.Name && b.width == width &&
		b.gen == gen {
		return b
	}
	ctx := &uireg.PresentCtx{Path: p.Path, Name: p.Name, Lines: strings.Split(strings.TrimSuffix(snap.String(), "\n"), "\n"), Width: width}
	err := registry.Call(func() error { p.Presenter.Build(ctx); return nil })
	if err != nil {
		h.Report(registry.KindView, "presenter."+p.Presenter.Name, err)
	}
	clear(st.parts)
	*b = presentBuild{buf: p.Buf, version: snap.Version(), path: p.Path, presenter: p.Presenter.Name, width: width, gen: gen, ctx: ctx,
		at: ctx.LineBlocks(snap.Lines()), err: err}
	return b
}

// Focus gives the presentation focus next frame.
func (st *presentState) Focus() { st.focus = true }

// ScrollTo brings a 0-based source line to the top next frame.
func (st *presentState) ScrollTo(line int) { st.top = line }

func newPresentState() *presentState {
	return &presentState{sel: -1, part: -1, parts: map[int][]presentPart{}, top: -1, gaps: map[string]float32{}, drawn: map[int]ui.ID{}, laid: map[int]ui.ID{}}
}

// presentFocus is the presentation that has focus, for its keys' commands.
type presentFocus struct {
	st     *presentState
	p      kit.Presentation
	h      plugin.Host
	opened bool // a thread was opened on a block this frame
}

// Presenter is the presenter service (kit.PresenterService): it draws buffers as their uireg.BufferPresenter
// builds them, for the editor's windows and any view of a plugin's own, and its keys (kit.PresentKeyMode) work on
// whichever presentation has focus.
type Presenter struct {
	kit.Disposer
	focus *presentFocus // the presentation with focus
}

func (x *Presenter) Manifest() plugin.Manifest { return plugin.Manifest{ID: "presenter"} }

func (x *Presenter) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Service{Name: kit.PresenterService, Impl: x}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range presentKeys() {
		x.Add(h.Keymap(k))
	}
	return nil
}

func (Presenter) NewPresent() kit.PresentState { return newPresentState() }

func (x *Presenter) Present(c *ui.Ctx, h plugin.Host, st kit.PresentState, p kit.Presentation, width float32) (int, bool) {
	return x.draw(c, h, st.(*presentState), p, width)
}

// commands are the keys of whatever presentation has focus.
func (x *Presenter) commands() []registry.Command {
	on := func(fn func(f *presentFocus)) func(registry.Ctx, registry.Args) error {
		return func(registry.Ctx, registry.Args) error {
			if f := x.focus; f != nil {
				fn(f)
			}
			return nil
		}
	}
	has := func(registry.Args) bool { return x.focus != nil }
	return []registry.Command{
		{ID: "present.next", Title: "Presentation: the next block", Available: has, Run: on(func(f *presentFocus) { f.move(1) })},
		{ID: "present.prev", Title: "Presentation: the block before", Available: has, Run: on(func(f *presentFocus) { f.move(-1) })},
		{ID: "present.comment", Title: "Presentation: open a thread on the selected block's lines", Available: has,
			Run: on(func(f *presentFocus) { f.comment(max(f.st.sel, 0), f.st.part) })},
		{ID: "present.source", Title: "Presentation: go to the selected block's source", Available: has, Run: on(func(f *presentFocus) {
			if f.p.Source == nil || len(f.st.blocks) == 0 {
				return
			}
			line := f.st.shown
			if f.st.sel >= 0 && f.st.sel < len(f.st.blocks) {
				line = f.st.blocks[f.st.sel].From
			}
			if pt, ok := f.st.selected(); ok {
				line = pt.from
			}
			f.p.Source(line)
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
		k.Mode = kit.PresentKeyMode
		out = append(out, k)
	}
	return out
}

// move selects the next block, or the one before: through a block's parts one at a time.
func (f *presentFocus) move(d int) {
	st := f.st
	if len(st.blocks) == 0 {
		return
	}
	if n := len(st.parts[st.sel]); st.sel >= 0 && st.part+d >= 0 && st.part+d < n {
		st.part += d
		st.reveal = true
		return
	}
	next := min(max(st.sel+d, 0), len(st.blocks)-1)
	if next == st.sel {
		return
	}
	st.sel, st.part = next, -1
	if n := len(st.parts[next]); n > 0 {
		st.part = 0
		if d < 0 {
			st.part = n - 1
		}
	}
	st.reveal = true
}

// comment opens a thread on block i's lines, or its part's (-1 for the whole block), when the presenter takes
// comments.
func (f *presentFocus) comment(i, part int) {
	if !f.p.Presenter.Comments || i >= len(f.st.blocks) {
		return
	}
	f.st.sel, f.st.part = i, part
	from, to := f.st.blocks[i].From, f.st.blocks[i].To
	if pt, ok := f.st.selected(); ok {
		from, to = pt.from, pt.to
	}
	a := registry.Args{"file": f.p.Path, "line": from + 1, "window": f.p.Window}
	if to > from {
		a["end_line"] = to + 1
	}
	f.opened = true
	if err := f.h.Run(context.Background(), "thread.new", a); err != nil {
		f.h.Notify(core.Notice{ID: "present", Severity: core.SevWarning, Source: "editor", AutoClear: true, Text: err.Error()})
	}
}

// blockDecor is what a presentation's decorations put on one block.
type blockDecor struct {
	all   []uireg.Decoration
	cards []uireg.Decoration // its Blocks, drawn in the margin
	signs []uireg.Decoration
	bg    *uireg.Decoration // a LineBg: its fill and edge
	label *uireg.Decoration // a VirtualText, as a label at its top right
	mark  string            // an Underline or Highlight's style, for its edge
}

// blockDecorations are the decorations of a presentation's buffer, put on the blocks holding their lines.
func blockDecorations(h plugin.Host, dctx uireg.DecorationCtx, b *presentBuild) []blockDecor {
	decor := make([]blockDecor, len(b.ctx.Blocks()))
	ds := h.Decorate(dctx)
	for _, line := range slices.Sorted(maps.Keys(ds)) {
		i := -1
		if line >= 0 && line < len(b.at) { // an EveryLine sign has no block to go on
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
			case uireg.Block:
				if d.Node != nil {
					bd.cards = append(bd.cards, d)
				}
			case uireg.Sign:
				if !d.OnHover { // a block has no line under the pointer to show it on
					bd.signs = append(bd.signs, d)
				}
			case uireg.LineBg:
				if bd.bg == nil {
					bd.bg = &d
				}
			case uireg.VirtualText:
				if bd.label == nil {
					bd.label = &d
				}
			case uireg.Underline, uireg.Highlight:
				if bd.mark == "" {
					bd.mark = d.Style
				}
			}
		}
	}
	return decor
}

// presentFrame is where a frame's blocks are, for building only those near the view, placing the cards and scrolling.
type presentFrame struct {
	c             *ui.Ctx
	st            *presentState
	body          ui.ID
	blocks        []*uireg.PresentBlock
	ids           []ui.ID   // each block's clickable, once built this frame
	tops          []float32 // each block's top in the content, and where the last ends
	view, content render.Rect
	okV, okC      bool // the view and its content were laid out last frame
}

// span is where block i's clickable top and bottom were last laid out.
func (fr *presentFrame) span(i int) (float32, float32, bool) {
	if _, laid := fr.st.laid[i]; laid && fr.ids[i] != 0 {
		r, _ := fr.c.Rect(fr.ids[i])
		return r.Min.Y, r.Max.Y, true
	}
	y := fr.content.Min.Y + fr.tops[i]
	return y + fr.blocks[i].Space, y + fr.tops[i+1] - fr.tops[i], fr.okC
}

// near is whether block i is built: it's near the view, or selected.
func (fr *presentFrame) near(i int) bool {
	if !fr.okV || !fr.okC || i == fr.st.sel {
		return true
	}
	n := len(fr.blocks)
	at := max(min(fr.c.Scroll(fr.body).Y, fr.tops[n]-fr.view.Dy()), 0)
	return fr.tops[i+1] >= at-fr.view.Dy() && fr.tops[i] <= at+2*fr.view.Dy()
}

// cardTop is where a card for block i's line goes: level with its part, else its block.
func (fr *presentFrame) cardTop(i, line int) (float32, bool) {
	if k := fr.st.partAt(i, line); k >= 0 {
		if r, ok := fr.c.Rect(fr.st.parts[i][k].id); ok {
			return r.Min.Y, true
		}
	}
	top, _, ok := fr.span(i)
	return top, ok
}

// draw draws a presentation: its blocks, numbered where something is, the selected one lit, and the margin of cards
// (the file's threads) level with their blocks. It reports the source line at its top, and whether the reader
// scrolled it there.
func (x *Presenter) draw(c *ui.Ctx, h plugin.Host, st *presentState, p kit.Presentation, width float32) (top int, scrolled bool) {
	th := c.Theme()
	snap := p.Buf.Snapshot()
	body := c.Next("body")
	if r, ok := c.Rect(body); ok {
		width = r.Dx()
	} else {
		h.Redraw() // laid out on a guess: again at its own width
	}
	margin := min(max(width*.38, 300), 520)
	docW := min(max(width-margin-120, 280), 760)
	built := st.build(h, p, snap, docW)
	if built.err != nil {
		c.Text("error", p.Presenter.Name+": "+built.err.Error(), ui.TextStyle{Color: th.C(theme.Error)})
		return st.shown, false
	}
	blocks := built.ctx.Blocks()
	st.blocks = blocks
	st.sel = min(st.sel, len(blocks)-1)
	dctx := uireg.DecorationCtx{Window: p.Window, Presenter: p.Presenter.Name, Now: c.Now(), Name: p.Name, Path: p.Path,
		Buffer: p.Buf, Snap: snap, Top: 0, Bottom: snap.Lines(), FileBytes: snap.Bytes, Wake: c.WakeAt}
	decor := blockDecorations(h, dctx, built)
	focus := &presentFocus{st: st, p: p, h: h}
	asked := false
	if st.focus {
		c.RequestFocus(body)
		st.focus, asked = false, true
	}
	if asked || c.FocusWithin(p.Root) {
		x.focus = focus
	} else if f := x.focus; f != nil && f.st == st {
		x.focus = nil
	}
	fr := &presentFrame{c: c, st: st, body: body, blocks: blocks, ids: make([]ui.ID, len(blocks))}
	var okV, okC bool
	fr.view, okV = c.Rect(body)
	fr.content, okC = c.Rect(ui.Child(body, "content"))
	fr.okV, fr.okC = okV && st.frames > 0, okC && st.frames > 0 // laid out once at least: not what was here before
	fr.tops = st.place(c, len(blocks))
	var cards []ui.ID
	scrolledBox(c, "body", &st.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, DefaultFocus: true, KeyMode: kit.PresentKeyMode, Role: ui.RoleList, Label: p.Name,
		Help: "editor/pane#present"}, func(c *ui.Ctx) {
		c.Row("page", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("doc", ui.Style{W: ui.Px(docW + 76), Pad: ui.Insets{L: 16, T: 28, B: 48}}, func(c *ui.Ctx) {
				gap := float32(0)
				for i, b := range blocks {
					if !fr.near(i) {
						gap += fr.tops[i+1] - fr.tops[i]
						continue
					}
					if gap > 0 {
						c.Box(fmt.Sprint("gap", i), ui.Style{H: ui.Px(gap)}, nil)
						gap = 0
					}
					st.drawn[i] = c.Next(fmt.Sprint("b", i))
					fr.ids[i] = presentBlock(c, h, focus, i, b, decor[i], docW, p.Kicker)
				}
				if gap > 0 {
					c.Box("gap", ui.Style{H: ui.Px(gap)}, nil)
				}
			})
			c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			if focus.opened {
				decor = blockDecorations(h, dctx, built) // with the thread a click just opened, so it takes the keys typed next
			}
			cards = presentMargin(c, st, p, decor, fr.cardTop, margin)
		})
	})
	st.frames++
	fr.scroll(built.ctx, cards)
	return fr.settled(p)
}

// scroll brings what was asked for into view: a source line to the top, the selected block or part, and the cards
// that want revealing.
func (fr *presentFrame) scroll(ctx *uireg.PresentCtx, cards []ui.ID) {
	c, st := fr.c, fr.st
	switch {
	case st.top >= 0 && fr.okV && fr.okC:
		if i := ctx.BlockAt(st.top); i >= 0 {
			if top, _, ok := fr.span(i); ok {
				st.at = 0
				if i > 0 {
					st.at = max(top-fr.content.Min.Y-28, 0) // as far below the top as the first block is
				}
				c.ScrollTo(fr.body, render.Point{Y: st.at})
				st.top, st.settle = -1, 2
			}
		}
	case st.reveal:
		if st.sel >= 0 && st.sel < len(fr.ids) {
			if _, laid := st.laid[st.sel]; !laid {
				c.WakeAt(c.Now()) // built this frame for the first time: shown once it's laid out
				break
			}
			target := fr.ids[st.sel]
			if pt, ok := st.selected(); ok {
				target = pt.id
			}
			c.ScrollIntoView(fr.body, target)
		}
		st.reveal = false
	}
	for _, id := range cards {
		c.ScrollIntoView(fr.body, id)
	}
}

// settled is the source line at the top now, and whether the reader scrolled it there.
func (fr *presentFrame) settled(p kit.Presentation) (int, bool) {
	st := fr.st
	at := fr.c.Scroll(fr.body).Y
	// The blocks were laid out at the scroll last drawn; the wheel may have moved it since.
	since := at - st.at
	st.shown = p.Buf.Snapshot().Lines() - 1
	if fr.okV {
		for i := range fr.blocks {
			if _, bottom, ok := fr.span(i); ok && bottom-since > fr.view.Min.Y+1 {
				st.shown = fr.blocks[i].From
				break
			}
		}
	}
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
func scrolledBox(c *ui.Ctx, key any, st *kit.Scroll, s ui.Style, body func(c *ui.Ctx)) {
	kit.Scrolled(c, key, st, s, ui.Style{}, func(c *ui.Ctx, _ func(string) ui.ID) { body(c) })
}

// blockMark is a line number and an edge colour, beside a block or one of its parts.
type blockMark struct {
	line int
	tone render.Color
}

// blockLook is how block i looks: its mark and each part's, where its cards are or where it's selected, its fill,
// and which part is selected (-1 for none, or the whole block).
type blockLook struct {
	mark    blockMark
	marks   []blockMark
	fill    render.Color
	sel     bool
	selPart int
}

func lookOf(th *theme.Resolved, st *presentState, i int, b *uireg.PresentBlock, bd blockDecor) blockLook {
	parts := st.parts[i]
	lk := blockLook{marks: make([]blockMark, len(parts)), sel: st.sel == i, selPart: -1}
	for _, d := range bd.cards {
		m := &blockMark{lk.mark.line, lk.mark.tone}
		k := st.partAt(i, d.Line)
		if k >= 0 {
			m = &lk.marks[k]
		}
		if m.line == 0 {
			m.line = d.Line + 1
		}
		if d.Style != "" && m.tone == (render.Color{}) {
			m.tone = kit.TokenColour(th, d.Style, m.tone)
		}
		if k < 0 {
			lk.mark = *m
		}
	}
	if lk.mark.tone == (render.Color{}) && bd.mark != "" {
		lk.mark.tone = kit.TokenColour(th, bd.mark, lk.mark.tone)
	}
	if lk.sel && st.part < len(parts) {
		lk.selPart = st.part
	}
	switch {
	case bd.bg != nil:
		lk.fill, lk.mark.tone = kit.TokenColour(th, bd.bg.Style, lk.fill), kit.TokenColour(th, bd.bg.Edge, lk.mark.tone)
	case lk.sel && lk.selPart < 0:
		lk.fill = th.C(tPresentSel)
	}
	if lk.sel && lk.selPart < 0 && lk.mark.line == 0 {
		lk.mark.line = b.From + 1
	}
	if lk.selPart >= 0 && lk.marks[lk.selPart].line == 0 {
		lk.marks[lk.selPart].line = parts[lk.selPart].from + 1
	}
	return lk
}

// presentBlock is one block: its number where a card is (or where it's selected), an edge in its card's or
// decoration's colour, its body, and a label at its top right. A click selects it and comments on it. A block whose
// body marks parts has these for each part, where it was last laid out, and a click on one is on it. It returns the
// ID of what's clicked, whose top a card for it is level with.
func presentBlock(c *ui.Ctx, h plugin.Host, f *presentFocus, i int, b *uireg.PresentBlock, bd blockDecor, width float32, kicker string) ui.ID {
	th := c.Theme()
	st := f.st
	parts := st.parts[i]
	lk := lookOf(th, st, i, b, bd)
	var id ui.ID
	var drawn []presentPart
	c.Box(fmt.Sprint("b", i), ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: b.Space}}, func(c *ui.Ctx) {
		id = c.Next("click")
		box, laid := c.Rect(id)
		// the part under the pointer, or at it
		under := func(at render.Point) int {
			for k, pt := range parts {
				if r, ok := c.Rect(pt.id); ok && at.X >= box.Min.X && at.X < box.Max.X && at.Y >= r.Min.Y && at.Y < r.Max.Y {
					return k
				}
			}
			return -1
		}
		hover := -1
		if in := c.Interaction(id); len(parts) > 0 && in.Hovered {
			hover = under(in.Pointer())
		}
		res := widget.Clickable(c, "click", ui.Style{Dir: ui.Row, W: ui.Flex(1), Fill: lk.fill, Selected: lk.sel, Role: ui.RoleListItem, Label: b.Label,
			NoHover: len(parts) > 0, Help: cmp.Or(f.p.Presenter.Help, "editor/pane#present")}, func(c *ui.Ctx) {
			c.Box("n", ui.Style{W: ui.Px(40), Pad: ui.Insets{R: 10, T: 4}, Main: ui.End, Dir: ui.Row}, func(c *ui.Ctx) {
				if len(bd.signs) > 0 {
					c.Row("signs", ui.Style{Gap: 2, Cross: ui.Center, Float: &ui.Float{Offset: render.Point{X: 2, Y: 3}}}, func(c *ui.Ctx) {
						for k, d := range bd.signs {
							kit.DrawSign(c, h, k, d, 13)
						}
					})
				}
				if lk.mark.line > 0 {
					c.Text("t", fmt.Sprint(lk.mark.line), kit.MonoFont(10.5, th.C(theme.FgGhost)))
				}
			})
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: lk.mark.tone}, nil)
			pad := ui.Insets{L: 12, R: 12, T: 2, B: 2}
			if bd.label != nil {
				pad.T, pad.B = 14, 8 // room for its label
			}
			c.Box("in", ui.Style{W: ui.Px(width + 24), Pad: pad}, func(c *ui.Ctx) {
				if b.Body != nil {
					b.Body(c, uireg.PresentDraw{Width: width, Decorations: bd.all, Kicker: kicker,
						Part: func(from, to int, id ui.ID) {
							drawn = append(drawn, presentPart{from: from, to: max(to, from), id: id})
						}})
				}
			})
			if laid {
				partMarks(c, parts, lk, box, hover)
			}
		})
		if res.Clicked {
			k := -1
			if len(parts) > 0 {
				k = under(res.Pointer())
			}
			st.sel, st.part = i, k
			f.comment(i, k)
		}
		if l := bd.label; l != nil {
			blockLabel(c, l, b.Space)
		}
	})
	if len(drawn) > 0 {
		st.parts[i] = drawn
	} else {
		delete(st.parts, i)
	}
	return id
}

// partMarks are a block's parts' washes (selected, under the pointer), numbers and edges, where they were last laid out.
func partMarks(c *ui.Ctx, parts []presentPart, lk blockLook, box render.Rect, hover int) {
	th := c.Theme()
	for k, pt := range parts {
		r, ok := c.Rect(pt.id)
		if !ok {
			continue
		}
		at := func(x float32) *ui.Float { return &ui.Float{Offset: render.Point{X: x, Y: r.Min.Y - box.Min.Y}} }
		wash := render.Color{}
		switch {
		case k == lk.selPart:
			wash = th.C(tPresentSel)
		case k == hover:
			wash = th.C(tPresentHover)
		}
		if wash.A > 0 {
			c.Box(fmt.Sprint("wash", k), ui.Style{W: ui.Px(box.Dx()), H: ui.Px(r.Dy()), Fill: wash, Float: at(0), Z: ui.LayerUnder}, nil)
		}
		if m := lk.marks[k]; m.line > 0 {
			c.Row(fmt.Sprint("n", k), ui.Style{W: ui.Px(40), Pad: ui.Insets{R: 10, T: 6}, Main: ui.End, Float: at(0)}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprint(m.line), kit.MonoFont(10.5, th.C(theme.FgGhost)))
			})
		}
		if m := lk.marks[k]; m.tone != (render.Color{}) {
			c.Box(fmt.Sprint("edge", k), ui.Style{W: ui.Px(2), H: ui.Px(r.Dy()), Fill: m.tone, Float: at(40)}, nil)
		}
	}
}

// blockLabel is a block's VirtualText, as a chip at its top right.
func blockLabel(c *ui.Ctx, l *uireg.Decoration, space float32) {
	th := c.Theme()
	edge := kit.TokenColour(th, l.Style, th.C(theme.FgFaint))
	c.Box("label", ui.Style{Float: &ui.Float{At: render.Point{X: 1, Y: 0}, Self: render.Point{X: 1, Y: .5}, Offset: render.Point{X: -8, Y: space}}},
		func(c *ui.Ctx) {
			c.Row("chip", ui.Style{Pad: ui.PadXY(8, 3), Gap: 6, Cross: ui.Center, Radius: 10, Fill: th.C(theme.BgBase), Stroke: theme.WithAlpha(edge, .5),
				Border: 1, Role: ui.RoleText, Label: l.Text}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: edge}, nil)
				c.Text("t", strings.ToUpper(l.Text), kit.KickerFont(edge))
			})
		})
}

// presentMargin is the blocks' cards, each level with its block or its line's part (or under the card before it),
// where cardTop says that was laid out. It returns the cards that want revealing.
func presentMargin(c *ui.Ctx, st *presentState, p kit.Presentation, decor []blockDecor, cardTop func(block, line int) (float32, bool), width float32) (reveal []ui.ID) {
	th := c.Theme()
	type entry struct {
		key   string
		block int
		d     uireg.Decoration
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
			if top, okB := cardTop(e.block, e.d.Line); ok && okB {
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
		if len(entries) == 0 && p.Presenter.Comments {
			c.Text("none", "Click a line to comment on it.", kit.UIFont(12, th.C(theme.FgGhost)))
		}
	})
	return reveal
}
