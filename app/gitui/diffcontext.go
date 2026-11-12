package gitui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// contextStep is how many hidden lines an expander shows a click, as GitHub's does.
const contextStep = 20

// hiddenGap is a run of unchanged new lines the diff hides: before hunk i (after the last when i is their count), new
// lines top to bottom, each old line its new line plus delta. Empty when bottom < top.
type hiddenGap struct {
	i, top, bottom, delta int
	first, last           bool // above the first hunk; below the last
}

func (g hiddenGap) size() int { return g.bottom - g.top + 1 }

// hiddenGaps are the gaps around d's hunks: one before each, and one after the last, to the file's end.
func hiddenGaps(d *diffFile) []hiddenGap {
	out := make([]hiddenGap, len(d.hunks)+1)
	prevNew, prevOld := 0, 0
	for i := range out {
		nextNew := len(d.lines) + 1
		if i < len(d.hunks) {
			nextNew, _ = hunkFirst(d.hunks[i].DiffHunk)
		}
		out[i] = hiddenGap{i: i, top: prevNew + 1, bottom: nextNew - 1, delta: prevOld - prevNew, first: i == 0, last: i == len(d.hunks)}
		if i < len(d.hunks) {
			prevNew, prevOld = hunkLast(d.hunks[i].DiffHunk)
		}
	}
	return out
}

// hunkFirst is a hunk's first new and old line; on a side it has none, the line after where it sits.
func hunkFirst(h gitx.DiffHunk) (newLine, oldLine int) {
	newLine, oldLine = h.NewStart+1, h.OldStart+1
	if h.NewLines > 0 {
		newLine = h.NewStart
	}
	if h.OldLines > 0 {
		oldLine = h.OldStart
	}
	return newLine, oldLine
}

// hunkLast is a hunk's last new and old line; on a side it has none, the line before where it sits.
func hunkLast(h gitx.DiffHunk) (newLine, oldLine int) {
	newLine, oldLine = h.NewStart, h.OldStart
	if h.NewLines > 0 {
		newLine = h.NewStart + h.NewLines - 1
	}
	if h.OldLines > 0 {
		oldLine = h.OldStart + h.OldLines - 1
	}
	return newLine, oldLine
}

// fileContext is what of a file's hidden lines show: by gap, how many down from its top and up from its bottom.
type fileContext struct {
	sig  string // the hunks it's for: another diff of the file starts over
	gaps map[int][2]int
}

// context is file's hidden lines shown, started over when its hunks aren't those it was for.
func (st *diffState) context(file string, hunks []diffHunk) *fileContext {
	var b strings.Builder
	for _, h := range hunks {
		b.WriteString(h.Header())
	}
	if st.ctx == nil {
		st.ctx = map[string]*fileContext{}
	}
	fc := st.ctx[file]
	if fc == nil || fc.sig != b.String() {
		fc = &fileContext{sig: b.String(), gaps: map[int][2]int{}}
		st.ctx[file] = fc
	}
	return fc
}

// show shows n more of gap's lines: down from its top, up from its bottom, or all of them.
func (fc *fileContext) show(g hiddenGap, dir string, n int) {
	s := fc.gaps[g.i]
	switch dir {
	case "down":
		s[0] += n
	case "up":
		s[1] += n
	default:
		s[0] = g.size()
	}
	fc.gaps[g.i] = s
}

// moreContext is diff.context.more: {path}'s gap {gap} (0 above the first hunk) {dir} up, down or all, by {lines};
// without a gap, the selected hunk's both ways, or with {all} every hidden line of the file.
func (x *Diff) moreContext(st *diffState, a registry.Args) error {
	file := a.String("path")
	if file == "" {
		file = st.sel.file
	}
	d := st.drawn[file]
	if d == nil || len(d.hunks) == 0 {
		return fmt.Errorf("no hunks of %q are drawn", file)
	}
	fc := st.context(file, d.hunks)
	gaps := hiddenGaps(d)
	n := a.Int("lines")
	if n <= 0 {
		n = contextStep
	}
	switch _, gap := a["gap"]; {
	case a["all"] == true:
		for _, g := range gaps {
			fc.show(g, "all", 0)
		}
	case gap:
		i := a.Int("gap")
		if i < 0 || i >= len(gaps) {
			return fmt.Errorf("%q has no gap %d", file, i)
		}
		fc.show(gaps[i], a.String("dir"), n)
	default:
		i := 0
		if st.sel.file == file && st.sel.hunk >= 0 {
			i = st.sel.hunk
		}
		fc.show(gaps[i], "up", n)
		fc.show(gaps[i+1], "down", n)
	}
	return nil
}

// gapRows are a gap's lines that show, with an expander between them while any are hidden.
func (x *Diff) gapRows(c *ui.Ctx, st *diffState, file string, d *diffFile, fc *fileContext, g hiddenGap, layout string,
	decor map[int][]uireg.Decoration) (reveal []ui.ID) {
	n := g.size()
	if n <= 0 {
		return nil
	}
	s := fc.gaps[g.i]
	down, up := min(s[0], n), min(s[1], n)
	lines := func(from, to int) {
		if from > to {
			return
		}
		spans := x.hiddenSpans(d, file, from, to)
		for k := from; k <= to; k++ {
			l := gitx.DiffLine{Op: ' ', Text: d.lines[k-1], New: k, Old: k + g.delta}
			x.contextRow(c, st, file, l, spans[k-from], layout)
			reveal = append(reveal, lineBlocks(c, decor, "c"+lineKey(l), k)...)
		}
	}
	c.Column(fmt.Sprint("gap", g.i), ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		if down+up >= n {
			lines(g.top, g.bottom)
			return
		}
		lines(g.top, g.top+down-1)
		x.expander(c, fc, g, g.top+down, g.bottom-up)
		lines(g.bottom-up+1, g.bottom)
	})
	return reveal
}

// expander is the row standing for a gap's hidden lines lo to hi: its buttons show them, contextStep at a time from
// the hunk above (↓) or below (↑), or all at once (↕) when that's no more.
func (x *Diff) expander(c *ui.Ctx, fc *fileContext, g hiddenGap, lo, hi int) {
	th := c.Theme()
	left := hi - lo + 1
	type step struct{ glyph, dir, tip string }
	var steps []step
	switch {
	case g.first:
		steps = []step{{"↑", "up", "Show the lines above"}}
	case g.last:
		steps = []step{{"↓", "down", "Show the lines below"}}
	case left <= contextStep:
		steps = []step{{"↕", "all", "Show the hidden lines"}}
	default:
		steps = []step{{"↓", "down", "Show lines below the hunk above"}, {"↑", "up", "Show lines above the hunk below"}}
	}
	c.Row("expand", ui.Style{W: ui.Flex(1), H: ui.Px(diffLineH + 2), Gap: 4, Pad: ui.Insets{L: 8, R: 14}, Cross: ui.Center, Fill: th.C(tDiffHunkBg)},
		func(c *ui.Ctx) {
			for _, s := range steps {
				from, to := lo, hi
				switch {
				case s.dir == "down":
					to = min(hi, lo+contextStep-1)
				case s.dir == "up":
					from = max(lo, hi-contextStep+1)
				}
				label := fmt.Sprintf("Show lines %d to %d", from, to)
				if widget.IconButton(c, s.dir, s.glyph, widget.IconButtonOpts{Label: label, Tooltip: s.tip + fmt.Sprintf(" (%d to %d)", from, to),
					Help: "files/diff#context", Size: 18}) {
					fc.show(g, s.dir, contextStep)
				}
			}
			c.Text("n", fmt.Sprintf("%d unchanged %s", left, kit.Plural(left, "line", "lines")), kit.MonoFont(10.5, th.C(theme.FgGhost)))
		})
}

// contextRow is a line the diff hid, shown: the same on both sides, numbered on each.
func (x *Diff) contextRow(c *ui.Ctx, st *diffState, file string, l gitx.DiffLine, spans []kit.Span, layout string) {
	th := c.Theme()
	h0, cross := x.rowFit()
	fit := x.codeFit(st, file)
	c.Row("c"+lineKey(l), ui.Style{W: ui.Flex(1), H: h0, Cross: cross, Role: ui.RoleListItem, Label: strings.TrimRight(l.Text, "\r\n")},
		func(c *ui.Ctx) {
			if layout == "unified" {
				c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1)}, nil)
				lineNo(c, "o", l.Old)
				lineNo(c, "n", l.New)
				c.Box("sign", ui.Style{W: ui.Px(16), H: ui.Px(diffLineH)}, nil)
				diffCode(c, spans, nil, render.Color{}, fit)
				return
			}
			for _, side := range []struct {
				key string
				n   int
			}{{"old", l.Old}, {"new", l.New}} {
				if side.key == "new" {
					c.Box("mid", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				}
				c.Row(side.key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Cross: cross, Clip: true}, func(c *ui.Ctx) {
					c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1)}, nil)
					lineNo(c, "n", side.n)
					diffCode(c, spans, nil, render.Color{}, fit)
				})
			}
		})
}

// hiddenSpans are a file's highlights of lines it shows only when asked, new lines from 1, made off the loop.
type hiddenSpans struct {
	mu    sync.Mutex
	spans map[int][]kit.Span
	asked map[[2]int]bool
}

// hiddenSpans are new lines from to to of d by highlight: plain text until they're highlighted off the loop.
func (x *Diff) hiddenSpans(d *diffFile, file string, from, to int) [][]kit.Span {
	out := make([][]kit.Span, to-from+1)
	hs := d.hidden
	hs.mu.Lock()
	missing := false
	for k := from; k <= to; k++ {
		if s, ok := hs.spans[k]; ok {
			out[k-from] = s
			continue
		}
		missing = true
		out[k-from] = []kit.Span{{Text: strings.TrimRight(d.lines[k-1], "\r\n")}}
	}
	ask := missing && d.styled && !hs.asked[[2]int{from, to}]
	if ask {
		if hs.asked == nil {
			hs.asked, hs.spans = map[[2]int]bool{}, map[int][]kit.Span{}
		}
		hs.asked[[2]int{from, to}] = true
	}
	hs.mu.Unlock()
	if ask {
		x.busy.Go(func() {
			hl := highlightRanges(x.h, file, d.newText, d.lines, [][2]int{{from - 1, to}})
			hs.mu.Lock()
			for k := from; k <= to; k++ {
				if hl != nil && hl[k-1] != nil {
					hs.spans[k] = hl[k-1]
				} else {
					hs.spans[k] = out[k-from]
				}
			}
			hs.mu.Unlock()
			x.h.Redraw()
		})
	}
	return out
}
