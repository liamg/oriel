package gitui

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tDiffBg       = theme.Declare("diff.bg", "bg.base")
	tDiffFiles    = theme.Declare("diff.files.bg", "bg.surface")
	tDiffFileSel  = theme.DeclareOn("diff.files.selected", "alpha(cyan, 10%)", "bg.surface")
	tDiffFileEdge = theme.Declare("diff.files.edge", "cyan")
	tDiffFileHead = theme.Declare("diff.file.bg", "bg.raised")
	tDiffFileMark = theme.Declare("diff.file.mark", "cyan")
	tDiffHunkBg   = theme.DeclareOn("diff.hunk.bg", "alpha(accent, 4%)", "bg.base")
	tDiffAddWord  = theme.DeclareOn("diff.add.word", "alpha(green, 25%)", "bg.base")
	tDiffDelWord  = theme.DeclareOn("diff.del.word", "alpha(red, 28%)", "bg.base")
	tDiffAddEdge  = theme.Declare("diff.add.edge", "green")
	tDiffDelEdge  = theme.Declare("diff.del.edge", "red")
	tDiffEmpty    = theme.DeclareOn("diff.empty", "alpha(fg, 2%)", "bg.base")
	tDiffSelected = theme.DeclareOn("diff.selected", "alpha(accent, 10%)", "bg.base")
	tDiffExplain  = theme.Declare("diff.explain", "cyan")
	tDiffReviewed = theme.Declare("diff.reviewed", "ok")
)

var diffCodeFont = typeset.FontSpec{Family: typeset.Mono, Size: 12.5}

const (
	diffLineH    = 20
	revealFrames = 8 // how long reveal waits for its target to be built and laid out
)

// view is the diff: the changed files at the left, each file's hunks in the middle, plugins' columns at the right.
func (x *Diff) view(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	st := x.state(vc.Instance)
	x.wrap = x.wraps()
	root := c.Next("diff")
	c.Row("diff", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tDiffBg), Role: ui.RoleRegion, Label: "Diff", Help: "files/diff"}, func(c *ui.Ctx) {
		trees := kit.GitOf(x.h)
		tree := trees.Tree(st.root)
		gs := tree.Counted()
		widget.Async(c, "load", kit.LoadState(tree.Load()), widget.AsyncOpts{Policy: widget.Block, Label: "Reading " + filepath.Base(st.root) + "'s changes",
			Empty: filepath.Base(st.root) + " isn't in a git repository: there are no changes to show.", IsEmpty: gs == nil,
			Retry: trees.Reload, Help: "files/diff"}, func(c *ui.Ctx) { x.loaded(c, vc, st, tree, gs, root) })
	})
	if st.seekLine() {
		c.WakeAt(c.Now())
	}
	if c.FocusWithin(root) || x.focus == nil {
		x.focus = st
	}
}

// loaded is the diff of a work tree whose status has been read.
func (x *Diff) loaded(c *ui.Ctx, vc uireg.ViewCtx, st *diffState, tree registry.GitTree, gs *gitx.State, root ui.ID) {
	th := c.Theme()
	c.Row("loaded", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		files := gs.Files(gitx.Changed)
		if st.head {
			files = gs.Whole() // a file staged and edited again is one change against HEAD
		}
		if st.sel.file != "" && !slices.ContainsFunc(files, func(f gitx.FileState) bool { return f.Path == st.sel.file }) {
			files = append(slices.Clone(files), gitx.FileState{Path: st.sel.file}) // asked for, and unchanged
		}
		st.files = files
		st.drawn, st.marks = map[string]*diffFile{}, map[string]map[string]bool{}
		for _, f := range files {
			d := x.made(tree.Root(), f.Path, st.head)
			if st.open[f.Path] || st.sel.file == f.Path {
				d, _ = x.diffFor(tree, gs, f, true, st.head)
			}
			if d != nil {
				st.drawn[f.Path] = d
			}
			st.marks[f.Path] = x.marks(st.root, f.Path)
		}
		if st.sel.file == "" && len(files) > 0 {
			st.sel.file = files[0].Path
			st.open[files[0].Path] = true
		}
		right := x.slotIDs(DiffRight, st.root)
		x.changes(c, st, files)
		c.Column("center", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			x.slots(c, kit.DiffHeader, st.root, vc.Width)
			x.body(c, vc, st, gs, root)
		})
		if len(right) > 0 {
			c.Column("right", ui.Style{W: ui.Px(360), H: ui.Flex(1), Fill: th.C(tDiffFiles)}, func(c *ui.Ctx) {
				x.slots(c, DiffRight, st.root, 360)
			})
		}
	})
}

// seekLine selects the line diff.open asked for once its file's hunks are drawn, of the side it asked for, else the
// file. It reports whether the selection moved.
func (st *diffState) seekLine() bool {
	if st.goLine <= 0 || st.sel.file == "" {
		return false
	}
	hs, ok := st.hunks[st.sel.file]
	if !ok {
		if d := st.drawn[st.sel.file]; d != nil && (d.err != nil || d.binary || len(d.hunks) == 0) {
			st.goLine = 0
		}
		return false
	}
	for i, h := range hs {
		for k, l := range h.Lines {
			if st.goOld && l.Old == st.goLine || !st.goOld && l.New == st.goLine {
				st.sel = diffSel{file: st.sel.file, hunk: i, line: k}
				st.goLine, st.reveal, st.centre = 0, true, true
				return true
			}
		}
	}
	st.goLine = 0
	return false
}

// remap is s, taken in from, at the same line of to: a diff made again may have other hunks. A line gone from it
// leaves the file selected.
func (s diffSel) remap(from, to []diffHunk) diffSel {
	if s.hunk < 0 || s.hunk >= len(from) || len(to) == 0 || &from[0] == &to[0] {
		return s
	}
	h := from[s.hunk]
	k := max(s.line, 0)
	if k >= len(h.Lines) {
		return s
	}
	want := h.Lines[k]
	for i, h := range to {
		for j, l := range h.Lines {
			if want.New > 0 && l.New == want.New || want.New == 0 && l.Old == want.Old {
				if s.line < 0 {
					j = -1
				}
				return diffSel{file: s.file, hunk: i, line: j}
			}
		}
	}
	return diffSel{file: s.file, hunk: -1, line: -1}
}

// slotIDs are the views registered under a prefix, by name, but those with nothing for root (uireg.Shown).
func (x *Diff) slotIDs(prefix, root string) []string {
	var out []string
	for _, id := range x.h.Views() {
		if !strings.HasPrefix(id, prefix) {
			continue
		}
		if v, ok := x.h.ViewByID(id); ok {
			if s, ok := uireg.As[uireg.Shown](v); ok && !s.Shown(root) {
				continue
			}
		}
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// slots draws the views registered under a prefix, each built for the diff's repo. DiffRight's share the column's
// height, each scrolling what it holds.
func (x *Diff) slots(c *ui.Ctx, prefix, root string, width float32) {
	h := ui.Size{}
	if prefix == DiffRight {
		h = ui.Flex(1)
	}
	for _, id := range x.slotIDs(prefix, root) {
		if v, ok := x.h.ViewByID(id); ok {
			c.Column(id, ui.Style{W: ui.Flex(1), H: h}, func(c *ui.Ctx) {
				c.Node(v.Build(uireg.ViewCtx{Width: width, Instance: root}))
			})
		}
	}
}

// fileCheck is a file's reviewed box: on when every hunk is marked, mixed when some are.
func fileCheck(d *diffFile, marks map[string]bool) (widget.CheckState, int) {
	if d == nil || len(d.hunks) == 0 {
		return widget.CheckOff, 0
	}
	n := 0
	for _, h := range d.hunks {
		if reviewed(h, marks) {
			n++
		}
	}
	switch n {
	case 0:
		return widget.CheckOff, 0
	case len(d.hunks):
		return widget.CheckOn, n
	}
	return widget.CheckMixed, n
}

// changes is the left column: every changed file with its reviewed box and size, what plugins add under it
// (authorship), and the keys.
func (x *Diff) changes(c *ui.Ctx, st *diffState, files []gitx.FileState) {
	th := c.Theme()
	add, del := 0, 0
	for _, f := range files {
		add, del = add+f.Add, del+f.Del
	}
	c.Column("changes", ui.Style{W: ui.Px(230), H: ui.Flex(1), Fill: th.C(tDiffFiles)}, func(c *ui.Ctx) {
		// The heading, a row a file, and the side slots, of which only those in view are built.
		key := func(i int) any {
			switch {
			case i == 0:
				return "\x00head"
			case i <= len(files):
				return files[i-1].Path
			}
			return "\x00side"
		}
		c.Items("top", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Role: ui.RoleList, Label: "Changed files"}, len(files)+2,
			ui.ItemsOpts{Estimate: 26, Key: key}, func(c *ui.Ctx, i int) {
				switch {
				case i == 0:
					c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("k", "CHANGES", kit.KickerFont(th.C(theme.FgFaint)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("n", fmt.Sprintf("%d %s · +%d −%d", len(files), kit.Plural(len(files), "file", "files"), add, del), kit.MonoFont(9.5, th.C(theme.FgGhost)))
					})
				case i <= len(files):
					x.changeRow(c, st, files[i-1])
				default:
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					x.slots(c, DiffSide, st.root, 230)
				}
			})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("keys", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Gap: 5}, func(c *ui.Ctx) {
			for i, pair := range [][2][2]string{{{"]h", "next hunk"}, {"r", "mark read"}}, {{"gc", "comment"}, {"ga", "ask"}}, {{"?", "explain"}}} {
				c.Row(i, ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
					for j, k := range pair {
						if k[0] != "" {
							kit.KeyHint(c, j, k[0], k[1])
						}
					}
				})
			}
		})
	})
}

func (x *Diff) changeRow(c *ui.Ctx, st *diffState, f gitx.FileState) {
	th := c.Theme()
	sel := st.sel.file == f.Path
	fill, edge := render.Color{}, render.Color{}
	if sel {
		fill, edge = th.C(tDiffFileSel), th.C(tDiffFileEdge)
	}
	d := st.drawn[f.Path]
	check, _ := fileCheck(d, st.marks[f.Path])
	c.Row(f.Path, ui.Style{W: ui.Flex(1), H: ui.Px(26), Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Row("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 12}, Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.Check(c, "ck", check, widget.CheckOpts{Label: "Reviewed: " + f.Path, Disabled: d == nil || len(d.hunks) == 0, Help: "files/diff#changes"}) {
				x.focus = st
				if err := x.reviewFile(st, f.Path, nil); err != nil {
					x.h.Log().Warn("diff", "err", err)
				}
			}
			fg := th.C(theme.FgMuted)
			if sel {
				fg = th.C(theme.Fg)
			}
			if widget.Clickable(c, "name", ui.Style{Dir: ui.Row, W: ui.Flex(1), Gap: 6, Cross: ui.Center, Role: ui.RoleListItem, Selected: sel, Label: f.Path,
				Tooltip: f.Path, Help: "files/diff#changes"}, func(c *ui.Ctx) {
				c.Text("n", filepath.Base(f.Path), kit.UIFont(12, fg))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				widget.DiffStat(c, "stat", f.Add, f.Del, widget.DiffStatOpts{Binary: f.Binary})
			}).Clicked {
				st.open[f.Path] = true
				st.sel = diffSel{file: f.Path, hunk: -1, line: -1}
				st.reveal, st.focus = true, true
			}
		})
	})
}

// body is the files, each with its header and, open, its hunks.
func (x *Diff) body(c *ui.Ctx, vc uireg.ViewCtx, st *diffState, gs *gitx.State, root ui.ID) {
	th := c.Theme()
	bodyID := c.Next("files")
	if st.focus {
		c.RequestFocus(bodyID)
		st.focus = false
	}
	if st.before == nil {
		st.before = map[string][]diffHunk{}
	}
	maps.Copy(st.before, st.hunks)
	st.order, st.lines, st.ids, st.hunks = nil, map[diffSel]int{}, map[diffSel]ui.ID{}, map[string][]diffHunk{}
	layout := x.layout()
	var reveal []ui.ID
	if len(st.files) == 0 {
		base := "its base"
		if gs.Base != "" {
			base = gs.Base + "@" + gs.BaseHash
		}
		c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.Pad(40), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "No changes against "+base+".", kit.UIFont(13, th.C(theme.FgFaint)))
		})
	}
	st.panLines(c)
	at := st.revealItem(c, bodyID)
	c.Items("files", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Focusable: true, DefaultFocus: true, KeyMode: DiffKeyMode, Role: ui.RoleList, Label: "Changes",
		Help: "files/diff#hunks"}, len(st.files), ui.ItemsOpts{Estimate: 35, Key: func(i int) any { return st.files[i].Path }, Reveal: at},
		func(c *ui.Ctx, i int) { reveal = append(reveal, x.fileSection(c, st, st.files[i], layout)...) })
	if st.reveal {
		st.revealSel(c, bodyID, at)
	}
	for _, id := range reveal {
		c.ScrollIntoView(bodyID, id)
	}
}

// revealItem is the file the list is to scroll to the top of the view, -1 for none: the selected one while it's
// to be shown and isn't built, or while it's shown whole, until it's at the top.
func (st *diffState) revealItem(c *ui.Ctx, bodyID ui.ID) int {
	_, laid := c.Rect(ui.Child(bodyID, st.sel.file))
	if !st.reveal || laid && !st.whole() {
		return -1
	}
	return slices.IndexFunc(st.files, func(f gitx.FileState) bool { return f.Path == st.sel.file })
}

// whole says the selection to be shown is a file, not a hunk or a line of it.
func (st *diffState) whole() bool { return st.sel.hunk < 0 && st.sel.line < 0 && st.goLine <= 0 }

// revealSel scrolls the list to the selection once it's laid out: a line diff.open went to the middle of the view, a
// step the least it takes; a file stays at the top of the view until its diff is made and it's settled there.
func (st *diffState) revealSel(c *ui.Ctx, bodyID ui.ID, at int) {
	id, ok := st.ids[st.sel]
	if !ok {
		id, ok = st.ids[diffSel{file: st.sel.file, hunk: st.sel.hunk, line: -1}]
	}
	if !ok {
		id, ok = st.ids[diffSel{file: st.sel.file, hunk: -1, line: -1}]
	}
	_, laid := c.Rect(id)
	making := st.open[st.sel.file] && st.drawn[st.sel.file] == nil // the diff's arrival draws the next frame
	arrived := st.making && !making                                // laid out last frame, before its diff
	st.making = making
	if !making {
		st.tries++
	}
	switch {
	case ok && laid && st.whole():
		st.reveal = making || arrived || !st.atTop(c, bodyID) && st.tries < revealFrames
	case ok && laid && st.centre:
		c.CentreInView(bodyID, id)
		st.reveal = false
	case ok && laid:
		c.ScrollIntoView(bodyID, id)
		st.reveal = false
	case making:
	case (ok || at >= 0) && st.tries < revealFrames:
		c.WakeAt(c.Now())
	default: // its file isn't listed, or never lays out
		st.reveal = false
	}
	if st.reveal && !making && ok && laid {
		c.WakeAt(c.Now())
	}
	if !st.reveal {
		st.tries, st.centre, st.making = 0, false, false
	}
}

// atTop says the selected file is where the list's Reveal leaves it: all of it in view, or its top at the view's.
func (st *diffState) atTop(c *ui.Ctx, bodyID ui.ID) bool {
	br, ok := c.Rect(bodyID)
	fr, ok2 := c.Rect(ui.Child(bodyID, st.sel.file))
	if !ok || !ok2 {
		return false
	}
	return fr.Min.Y >= br.Min.Y-0.5 && (fr.Max.Y <= br.Max.Y+0.5 || fr.Min.Y <= br.Min.Y+0.5)
}

// fileSection is a file's header (its path, size and how much is reviewed; a click shows or folds it) and, open,
// its hunks. It returns the decorations that asked to be brought into view.
func (x *Diff) fileSection(c *ui.Ctx, st *diffState, f gitx.FileState, layout string) (reveal []ui.ID) {
	th := c.Theme()
	open := st.open[f.Path]
	d := st.drawn[f.Path]
	c.Column(f.Path, ui.Style{W: ui.Flex(1), Role: ui.RoleGroup, Label: f.Path}, func(c *ui.Ctx) {
		st.ids[diffSel{file: f.Path, hunk: -1, line: -1}] = c.Next("head")
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.Insets{R: 14}, Gap: 10, Cross: ui.Center, Fill: th.C(tDiffFileHead)}, func(c *ui.Ctx) {
			if widget.Clickable(c, "toggle", ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 14}, Gap: 10, Cross: ui.Center,
				Label: f.Path, Help: "files/diff#files"}, func(c *ui.Ctx) {
				c.Box("mark", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: th.C(tDiffFileMark)}, nil)
				c.Text("p", f.Path, kit.MonoFont(12, th.C(theme.Fg)))
				if f.Old != "" {
					c.Text("old", "← "+f.Old, kit.MonoFont(11, th.C(theme.FgFaint)))
				}
				widget.DiffStat(c, "stat", f.Add, f.Del, widget.DiffStatOpts{Binary: f.Binary})
				if check, n := fileCheck(d, st.marks[f.Path]); check == widget.CheckOn {
					widget.Chip(c, "rv", "reviewed", th.C(tDiffReviewed), "files/diff#review")
				} else if d != nil && len(d.hunks) > 0 {
					c.Text("rv", fmt.Sprintf("%d/%d hunks reviewed", n, len(d.hunks)), kit.MonoFont(10, th.C(theme.FgGhost)))
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if !open {
					c.Text("x", "⌄ expand", kit.MonoFont(11, th.C(theme.FgFaint)))
				}
			}).Clicked {
				st.open[f.Path] = !open
				st.sel = diffSel{file: f.Path, hunk: -1, line: -1}
			}
			if open && (d == nil || d.images == nil) {
				if widget.ToggleChip(c, "wrap", "wrap", x.wrap, widget.ToggleChipOpts{Label: "Wrap long lines", Help: "files/diff#wrap",
					Tooltip: "Wrap long lines, or scroll across them (editor.wrap)"}) != x.wrap {
					x.h.Run(context.Background(), "diff.wrap", registry.Args{"wrap": !x.wrap})
				}
				active := 0
				if layout == "unified" {
					active = 1
				}
				if widget.Segmented(c, "layout", []string{"split", "unified"}, &active, widget.SegmentedOpts{Label: "Layout", Help: "files/diff#layout"}) {
					x.h.Run(context.Background(), "diff.layout", registry.Args{"layout": []string{"split", "unified"}[active]})
				}
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if !open {
			return
		}
		if d != nil && d.binary && d.images != nil {
			x.imageDiff(c, st, f.Path, d.images)
			return
		}
		if d == nil {
			if st.waiting == nil {
				st.waiting = map[string]time.Time{}
			}
			if _, ok := st.waiting[f.Path]; !ok {
				st.waiting[f.Path] = c.Now()
			}
			c.Row("wait", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(62, 10)}, func(c *ui.Ctx) {
				widget.Async(c, "diff", widget.LoadState{Phase: widget.Pending, Since: st.waiting[f.Path]}, widget.AsyncOpts{
					Label: "Reading the diff", Help: "files/diff#hunks"}, nil)
			})
			return
		}
		delete(st.waiting, f.Path)
		note := ""
		switch {
		case d.err != nil:
			note = d.err.Error()
		case d.binary:
			note = "A binary file: no lines to show."
		case len(d.hunks) == 0:
			note = "No changes."
		}
		if note != "" {
			c.Row("note", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(62, 10)}, func(c *ui.Ctx) { c.Text("t", note, kit.UIFont(12, th.C(theme.FgFaint))) })
			return
		}
		if f.Path == st.sel.file {
			st.sel = st.sel.remap(st.before[f.Path], d.hunks)
		}
		st.hunks[f.Path] = d.hunks
		buf := x.newBuffer(st, f.Path, d)
		snap := buf.Snapshot()
		decor := x.h.Decorate(uireg.DecorationCtx{Window: DiffView + ":" + st.root, Presenter: diffPresenter, Now: c.Now(), Name: f.Path,
			Path: filepath.Join(st.root, filepath.FromSlash(f.Path)), Buffer: buf, Snap: snap, Top: 0, Bottom: snap.Lines(), FileBytes: snap.Bytes,
			Wake: c.WakeAt})
		gaps := hiddenGaps(d)
		fc := st.context(f.Path, d.hunks)
		for i, h := range d.hunks {
			reveal = append(reveal, x.gapRows(c, st, f.Path, d, fc, gaps[i], layout, decor)...)
			reveal = append(reveal, x.hunk(c, st, f.Path, i, h, layout, decor)...)
		}
		reveal = append(reveal, x.gapRows(c, st, f.Path, d, fc, gaps[len(d.hunks)], layout, decor)...)
	})
	return reveal
}

// hunk is a hunk's header (explain, its @@ line and function, the reviewed box) and its lines, split or unified,
// with the decorations of each new line's under it.
func (x *Diff) hunk(c *ui.Ctx, st *diffState, file string, i int, h diffHunk, layout string, decor map[int][]uireg.Decoration) (reveal []ui.ID) {
	th := c.Theme()
	at := diffSel{file: file, hunk: i, line: -1}
	st.order = append(st.order, at)
	st.lines[at] = len(h.Lines)
	selHunk := st.sel.file == file && st.sel.hunk == i
	done := reviewed(h, st.marks[file])
	st.ids[at] = c.Next(i)
	c.Column(i, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		fill := th.C(tDiffHunkBg)
		if selHunk && st.sel.line < 0 {
			fill = th.C(tDiffSelected)
		}
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(26), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
			q := th.C(tDiffExplain)
			if widget.Clickable(c, "explain", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 9, Stroke: q, Border: 1, Main: ui.Center, Cross: ui.Center,
				Disabled: !x.h.Available(ExplainUnit, nil), Label: "Explain", Tooltip: "Explain this code (?)", Help: "files/diff#explain"}, func(c *ui.Ctx) {
				c.Text("t", "?", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.Bold}, Color: q})
			}).Clicked {
				st.sel = at
				if err := x.explain(st, nil); err != nil {
					x.h.Log().Warn("diff", "err", err)
				}
			}
			if widget.Clickable(c, "at", ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Flex(1), Cross: ui.Center, Role: ui.RoleListItem, Selected: selHunk,
				Label: h.Header(), Help: "files/diff#hunks"}, func(c *ui.Ctx) {
				c.Text("t", strings.TrimSpace(h.Header()+" "+h.fn), kit.MonoFont(11, th.C(theme.FgFaint)))
			}).Clicked {
				st.sel, st.focus = at, true
			}
			state, label := widget.CheckOff, "Mark reviewed"
			if done {
				state, label = widget.CheckOn, "Reviewed"
			}
			if widget.Check(c, "reviewed", state, widget.CheckOpts{Text: label, Help: "files/diff#review"}) {
				st.sel = at
				if err := x.reviewHunk(st, at); err != nil {
					x.h.Log().Warn("diff", "err", err)
				}
			}
		})
		blocks := func(key any, line int) { reveal = append(reveal, lineBlocks(c, decor, key, line)...) }
		if layout == "unified" {
			for k, l := range h.Lines {
				x.unifiedRow(c, st, at, k, h, lineDecor{decor, l.New})
				if l.New > 0 {
					blocks(k, l.New)
				}
			}
			return
		}
		for r, p := range splitPairs(h.Lines) {
			ld := lineDecor{decor: decor}
			if p[1] >= 0 {
				ld.n = h.Lines[p[1]].New
			}
			x.splitRow(c, st, at, r, p, h, ld)
			if p[1] >= 0 {
				blocks(r, h.Lines[p[1]].New)
			}
		}
	})
	return reveal
}

// lineBlocks draws the Block decorations of new line (from 1) under it, and returns those that asked to be brought
// into view.
func lineBlocks(c *ui.Ctx, decor map[int][]uireg.Decoration, key any, line int) (reveal []ui.ID) {
	for _, d := range decor[line-1] {
		if d.Kind != uireg.Block || d.Node == nil || line <= 0 {
			continue
		}
		k := fmt.Sprint(key, ".", d.Priority)
		if f, ok := d.Node.(*ui.Func); ok && f.Key != nil {
			k = fmt.Sprint(f.Key)
		}
		id := c.Next(k)
		c.Box(k, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 62, R: 14, T: 8, B: 10}}, func(c *ui.Ctx) {
			if f, ok := d.Node.(*ui.Func); ok {
				f.Fn(c)
				return
			}
			c.Node(d.Node)
		})
		if d.Reveal {
			reveal = append(reveal, id)
		}
	}
	return reveal
}

// splitPairs are a hunk's lines side by side: a context line on both sides, a change's removed lines beside its
// added ones; -1 where a side has none.
func splitPairs(lines []gitx.DiffLine) [][2]int {
	var out [][2]int
	for i := 0; i < len(lines); {
		if lines[i].Op == ' ' {
			out = append(out, [2]int{i, i})
			i++
			continue
		}
		var dels, adds []int
		for ; i < len(lines) && lines[i].Op == '-'; i++ {
			dels = append(dels, i)
		}
		for ; i < len(lines) && lines[i].Op == '+'; i++ {
			adds = append(adds, i)
		}
		for k := range max(len(dels), len(adds)) {
			p := [2]int{-1, -1}
			if k < len(dels) {
				p[0] = dels[k]
			}
			if k < len(adds) {
				p[1] = adds[k]
			}
			out = append(out, p)
		}
	}
	return out
}

// lineTones are a line's fill, edge and changed words' colour, by what it does.
func lineTones(th *theme.Resolved, op byte) (fill, edge, word render.Color) {
	switch op {
	case '-':
		return th.C(theme.DiffDel), th.C(tDiffDelEdge), th.C(tDiffDelWord)
	case '+':
		return th.C(theme.DiffAdd), th.C(tDiffAddEdge), th.C(tDiffAddWord)
	}
	return render.Color{}, render.Color{}, render.Color{}
}

// lineDecor is a diff row's new-side line, n from 1 (0 for a row that has none: a removal), in its file's decorations.
type lineDecor struct {
	decor map[int][]uireg.Decoration
	n     int
}

// hoverSign draws, at the left of a line's gutter, the top of its OnHover signs (a + that comments on it), its own
// and those on every line (uireg.EveryLine), while the pointer is on row, as the editor's gutter does.
func (x *Diff) hoverSign(c *ui.Ctx, row ui.ID, ld lineDecor) {
	if ld.n <= 0 {
		return
	}
	line := ld.n - 1
	var signs []uireg.Decoration
	for _, d := range ld.decor[uireg.EveryLine] {
		if d.Kind == uireg.Sign && d.OnHover {
			signs = append(signs, d)
		}
	}
	for _, d := range ld.decor[line] {
		if d.Kind == uireg.Sign && d.OnHover {
			signs = append(signs, d)
		}
	}
	if len(signs) == 0 || !c.PointerOver(row) {
		return
	}
	for i, d := range signs {
		if d.Line == uireg.EveryLine {
			signs[i] = d.OnLine(line)
		}
	}
	d, _ := uireg.TopSign(signs)
	c.Row("hover", ui.Style{W: ui.Px(14), H: ui.Px(diffLineH), Main: ui.Center, Cross: ui.Center, Float: &ui.Float{Offset: render.Point{X: 4}}},
		func(c *ui.Ctx) { kit.DrawSign(c, x.h, "sign", d, 12) })
}

func (x *Diff) unifiedRow(c *ui.Ctx, st *diffState, at diffSel, k int, h diffHunk, ds lineDecor) {
	th := c.Theme()
	l := h.Lines[k]
	me := diffSel{file: at.file, hunk: at.hunk, line: k}
	sel := st.sel == me
	fill, edge, word := lineTones(th, l.Op)
	if sel {
		fill = th.C(tDiffSelected)
	}
	key := lineKey(l)
	row := c.Next(key)
	st.ids[me] = row
	h0, cross := x.rowFit()
	if widget.Clickable(c, key, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: h0, Cross: cross, Fill: fill, Role: ui.RoleListItem, Selected: sel,
		Label: strings.TrimRight(l.Text, "\r\n"), Help: "files/diff#hunks"}, func(c *ui.Ctx) {
		x.hoverSign(c, row, ds)
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		lineNo(c, "o", l.Old)
		lineNo(c, "n", l.New)
		c.Box("sign", ui.Style{W: ui.Px(16), H: ui.Px(diffLineH), Cross: ui.Center}, func(c *ui.Ctx) {
			if l.Op != ' ' {
				c.Text("t", string(l.Op), ui.TextStyle{Font: diffCodeFont, Color: edge})
			}
		})
		diffCode(c, h.spans[k], h.marks[k], word, x.codeFit(st, at.file))
	}).Clicked {
		st.sel = me
	}
}

func (x *Diff) splitRow(c *ui.Ctx, st *diffState, at diffSel, r int, p [2]int, h diffHunk, ds lineDecor) {
	th := c.Theme()
	k := p[1]
	if k < 0 {
		k = p[0]
	}
	me := diffSel{file: at.file, hunk: at.hunk, line: k}
	sel := st.sel == me || st.sel.file == at.file && st.sel.hunk == at.hunk && st.sel.line == p[0] && p[0] >= 0
	key := lineKey(h.Lines[k])
	row := c.Next(key)
	st.ids[me] = row
	if p[0] >= 0 && p[0] != k {
		st.ids[diffSel{file: at.file, hunk: at.hunk, line: p[0]}] = st.ids[me]
	}
	side := func(c *ui.Ctx, key string, i int, old bool) {
		if i < 0 {
			c.Box(key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tDiffEmpty)}, nil)
			return
		}
		l := h.Lines[i]
		op := l.Op
		if op == '-' && !old || op == '+' && old {
			op = ' '
		}
		fill, edge, word := lineTones(th, op)
		if sel {
			fill = th.C(tDiffSelected)
		}
		_, cross := x.rowFit()
		c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Cross: cross, Fill: fill, Clip: true}, func(c *ui.Ctx) {
			if !old {
				x.hoverSign(c, row, ds)
			}
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
			n := l.New
			if old {
				n = l.Old
			}
			lineNo(c, "n", n)
			diffCode(c, h.spans[i], h.marks[i], word, x.codeFit(st, at.file))
		})
	}
	label := ""
	if k >= 0 {
		label = strings.TrimRight(h.Lines[k].Text, "\r\n")
	}
	h0, _ := x.rowFit()
	if widget.Clickable(c, key, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: h0, Role: ui.RoleListItem, Selected: sel, Label: label,
		Help: "files/diff#hunks"}, func(c *ui.Ctx) {
		side(c, "old", p[0], true)
		c.Box("mid", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
		side(c, "new", p[1], false)
	}).Clicked {
		st.sel = me
	}
}

// lineKey is a diff line's row's key: its line in the file (the new side's, or the old side's for a removal) and its
// text. Keyed by where it sits in its hunk, a row would be another line once the diff is made again with lines come
// in above it, and a press on it would let go on that one. Keyed by both, a press stays on the line it was on, and
// comes to nothing if that line moved or changed meanwhile: it never lands on another.
func lineKey(l gitx.DiffLine) string {
	if l.New > 0 {
		return fmt.Sprintf("n%d %s", l.New, l.Text)
	}
	return fmt.Sprintf("o%d %s", l.Old, l.Text)
}

// lineNo is a line's number in its gutter, blank for none.
func lineNo(c *ui.Ctx, key string, n int) {
	c.Row(key, ui.Style{W: ui.Px(44), H: ui.Px(diffLineH), Pad: ui.Insets{R: 10}, Main: ui.End, Cross: ui.Center}, func(c *ui.Ctx) {
		if n > 0 {
			c.Text("t", fmt.Sprint(n), ui.TextStyle{Font: diffCodeFont, Color: c.Theme().C(theme.FgGhost)})
		}
	})
}
