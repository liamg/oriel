package widget

import (
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

type ListOpts struct {
	RowHeight float32 // 22 by default
	Height    ui.Size // grows to fill by default
	Help      string
	Label     string // its accessible name
	KeyMode   string // the keymap mode it takes keys in; "list" by default

	role, rowRole ui.Role           // a tree is a list with tree roles
	expanded      func(i int) *bool // a tree row's expanded state
}

// listLast is the selection the list last showed, so it follows it only when it moves.
type listLast struct {
	sel, n int
	seen   bool
}

type ListResult struct {
	Changed   bool // the selection moved
	Activated int  // the row clicked or opened with Enter, or -1
}

// List is a virtual list of n rows with a selection in *sel: only the rows in view are built. Keys: j/k or
// Down/Up move, g/G or Home/End jump, PageDown/PageUp (and Ctrl-d/u) move a page, Enter opens. A click selects
// and opens. The selection stays in view.
func List(c *ui.Ctx, key any, n int, sel *int, o ListOpts, row func(c *ui.Ctx, i int, selected bool)) ListResult {
	res := ListResult{Activated: -1}
	rh := o.RowHeight
	if rh == 0 {
		rh = 22
	}
	id := c.Next(key)
	in := c.Interaction(id)
	page := 10
	if r, ok := c.Rect(id); ok {
		page = max(1, int(r.Dy()/rh)-1)
	}
	old := *sel
	for _, k := range in.Keys {
		switch {
		case hasKey([]render.KeyEvent{k}, "j", "Down"):
			*sel++
		case hasKey([]render.KeyEvent{k}, "k", "Up"):
			*sel--
		case hasKey([]render.KeyEvent{k}, "g", "Home"):
			*sel = 0
		case hasKey([]render.KeyEvent{k}, "G", "End"):
			*sel = n - 1
		case k.Key == "PageDown" || k.Key == "d" && k.Mods == render.ModCtrl:
			*sel += page
		case k.Key == "PageUp" || k.Key == "u" && k.Mods == render.ModCtrl:
			*sel -= page
		case k.Key == "CR":
			res.Activated = *sel
		}
	}
	*sel = clampInt(*sel, 0, max(n-1, 0))
	if n == 0 {
		*sel = 0
	}
	// Bring the selection into view when it moves, by key or by the caller; the wheel scrolls away from it freely.
	last := ui.State[listLast](c, ui.Child(id, "last"))
	moved := !last.seen || last.sel != *sel || last.n != n
	last.sel, last.n, last.seen = *sel, n, true
	if r, ok := c.Rect(id); ok && n > 0 && moved {
		top, bottom := float32(*sel)*rh, float32(*sel+1)*rh
		scroll := c.Scroll(id).Y
		switch {
		case top < scroll:
			c.ScrollTo(id, render.Point{Y: top})
		case bottom > scroll+r.Dy():
			c.ScrollTo(id, render.Point{Y: bottom - r.Dy()})
		}
	}
	h := o.Height
	if h == (ui.Size{}) {
		h = ui.Flex(1)
	}
	selected := *sel
	role, rowRole := ui.RoleList, ui.RoleListItem
	if o.role != "" {
		role, rowRole = o.role, o.rowRole
	}
	// The selection's highlight is drawn behind the rows and glides between them when it moves.
	ls := ui.State[listState](c, ui.Child(id, "selection"))
	if ls.to != selected || !ls.seen {
		at := float32(selected)
		if ls.seen {
			at = ls.from + (float32(ls.to)-ls.from)*c.Motion(listKey{id}, 1)
		}
		ls.from, ls.to, ls.seen = at, selected, true
		c.Motion(listKey{id}, 0)
		c.ResetMotion(listKey{id}, 0)
	}
	p := c.Motion(listKey{id}, 1)
	behind := func(lc *ui.Ctx, top float32) {
		if n == 0 {
			return
		}
		r, ok := c.Rect(id)
		if !ok {
			return
		}
		y := top + (ls.from+(float32(ls.to)-ls.from)*p)*rh
		at := render.Point{X: r.Min.X, Y: y}
		lc.Row("selection", ui.Style{W: ui.Px(r.Dx()), H: ui.Px(rh), Fill: col(c, tRowSelected), Z: ui.LayerUnder, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: col(c, tRowEdge)}, nil)
		})
	}
	mode := o.KeyMode
	if mode == "" {
		mode = "list"
	}
	c.ListBehind(key, ui.Style{W: ui.Flex(1), H: h, Focusable: true, Help: o.Help, KeyMode: mode, Role: role, Label: o.Label}, n, rh, behind, func(rc *ui.Ctx, i int) {
		item := rc.Next("item")
		ri := rc.Interaction(item)
		if ri.Clicked {
			*sel, selected = i, i
			res.Activated = i
			rc.Focus(id)
		}
		fill := render.Color{}
		if ri.Hovered && i != selected {
			fill = col(c, tRowHover)
		}
		var expanded *bool
		if o.expanded != nil {
			expanded = o.expanded(i)
		}
		rc.Row("item", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: fill, EaseFill: true, Cross: ui.Center, Interactive: true, NoHover: true,
			Cursor: render.CursorPointer, Role: rowRole, Selected: i == selected, Expanded: expanded}, func(rc *ui.Ctx) {
			rc.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1)}, nil)
			row(rc, i, i == selected)
		})
	})
	res.Changed = *sel != old
	return res
}

// listState is where the selection's highlight glides from and to.
type listState struct {
	from float32
	to   int
	seen bool
}

type listKey struct{ id ui.ID }

// TreeItem is one node of a tree. ID must be unique in the tree.
type TreeItem struct {
	ID        string
	Label     string
	Note      string       // dim, after the label: "← amount.go", "main · ↑2"
	Tone      render.Color // the label's colour, if not the list's
	Strike    bool         // the label struck through, as a deleted file
	Glyph     string       // a small mark before the label, in GlyphTone: the explorer's ● ○
	GlyphTone render.Color
	Swatch    render.Color // a small square before the label, as the explorer's file icons
	Mark      string       // right-aligned, as the explorer's M/A/D
	MarkTone  render.Color
	Section   bool // a heading: small capitals, as STAGED
	Open      bool // expanded until the user collapses it
	Children  []TreeItem
	Help      string
}

type TreeOpts struct {
	RowHeight float32 // 22 by default
	Height    ui.Size
	Help      string
	Label     string
	KeyMode   string // the keymap mode it takes keys in, for keys its owner maps; "list" by default
	// Trailing draws at the right of each row, before Mark: counts, bars, badges.
	Trailing func(c *ui.Ctx, it TreeItem, selected bool)
}

type TreeResult struct {
	Selected  string // the selected item's ID
	Activated string // an item opened this frame, or ""
	Toggled   string // an item expanded or collapsed this frame, or ""
}

type treeState struct {
	open  map[string]bool // only what the user toggled; the rest follow their item's Open
	sel   int
	selID string // the selected item, which the selection follows when rows move
}

func (st *treeState) isOpen(it *TreeItem) bool {
	if o, ok := st.open[it.ID]; ok {
		return o
	}
	return it.Open
}

type flatItem struct {
	item   *TreeItem
	depth  int
	parent int
}

// Tree is an expanding tree over a virtual list (base.css .tree). Keys are the list's, plus l or Right to
// expand (then step in), h or Left to collapse (then step out), and Enter to open a leaf or toggle a folder.
// A click on a folder toggles it; on a leaf, opens it.
func Tree(c *ui.Ctx, key any, roots []TreeItem, o TreeOpts) TreeResult {
	id := c.Next(key)
	st := ui.State[treeState](c, id)
	if st.open == nil {
		st.open = map[string]bool{}
	}
	var flat []flatItem
	var walk func(items []TreeItem, depth, parent int)
	walk = func(items []TreeItem, depth, parent int) {
		for i := range items {
			flat = append(flat, flatItem{&items[i], depth, parent})
			if st.isOpen(&items[i]) {
				walk(items[i].Children, depth+1, len(flat)-1)
			}
		}
	}
	walk(roots, 0, -1)
	if st.selID != "" && (st.sel >= len(flat) || flat[st.sel].item.ID != st.selID) {
		for i, f := range flat {
			if f.item.ID == st.selID {
				st.sel = i
				break
			}
		}
	}
	var res TreeResult
	toggle := func(i int) {
		it := flat[i].item
		st.open[it.ID] = !st.isOpen(it)
		res.Toggled = it.ID
	}
	// The tree takes l/h/Enter; the list under it, reading the same node's keys, moves the selection.
	in := c.Interaction(id)
	for _, k := range in.Keys {
		if st.sel >= len(flat) {
			break
		}
		cur := flat[st.sel]
		switch {
		case hasKey([]render.KeyEvent{k}, "l", "Right"):
			if len(cur.item.Children) > 0 {
				if !st.isOpen(cur.item) {
					toggle(st.sel)
				} else {
					st.sel++
				}
			}
		case hasKey([]render.KeyEvent{k}, "h", "Left"):
			if len(cur.item.Children) > 0 && st.isOpen(cur.item) {
				toggle(st.sel)
			} else if cur.parent >= 0 {
				st.sel = cur.parent
			}
		case k.Key == "CR":
			if len(cur.item.Children) > 0 {
				toggle(st.sel)
			} else {
				res.Activated = cur.item.ID
			}
		}
	}
	if res.Toggled != "" {
		// Re-flatten: the rows changed under the selection.
		flat = flat[:0]
		walk(roots, 0, -1)
	}
	rh := o.RowHeight
	if rh == 0 {
		rh = 22
	}
	label := uiText(c, 12.5, typeset.Normal, col(c, tRowFg))
	expanded := func(i int) *bool {
		if len(flat[i].item.Children) == 0 {
			return nil
		}
		open := st.isOpen(flat[i].item)
		return &open
	}
	lo := ListOpts{RowHeight: rh, Height: o.Height, Help: o.Help, Label: o.Label, KeyMode: o.KeyMode, role: ui.RoleTree, rowRole: ui.RoleTreeItem,
		expanded: expanded}
	lr := List(c, key, len(flat), &st.sel, lo, func(rc *ui.Ctx, i int, selected bool) {
		it := flat[i].item
		t := label
		if it.Tone.A > 0 {
			t.Color = it.Tone
		}
		if selected && it.Tone.A == 0 {
			t.Color = col(c, tRowSelectedFg)
		}
		if it.Section {
			t = monoText(9.5, typeset.SemiBold, col(c, tLabel), .12)
		}
		rc.Row("row", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 10 + float32(flat[i].depth)*14, R: 10}, Gap: 6, Cross: ui.Center, Help: it.Help}, func(rc *ui.Ctx) {
			if len(it.Children) > 0 && !it.Section {
				caret := "▸"
				if st.isOpen(it) {
					caret = "▾"
				}
				rc.Box("caret", ui.Style{Role: ui.RoleNone}, func(rc *ui.Ctx) { rc.Paint("glyph", ui.Px(8), ui.Px(14), caretPainter(c, caret, t)) })
			}
			if it.Glyph != "" {
				rc.Text("glyph", it.Glyph, monoText(9, typeset.Normal, it.GlyphTone, 0))
			}
			if it.Swatch.A > 0 {
				rc.Box("swatch", ui.Style{W: ui.Px(10), H: ui.Px(10), Fill: alpha(it.Swatch, .85), Radius: 2}, nil)
			}
			label := it.Label
			if it.Section {
				label = strings.ToUpper(label)
			}
			rc.Row("name", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center, Clip: true}, func(rc *ui.Ctx) {
				rc.Box("label", ui.Style{}, func(rc *ui.Ctx) {
					rc.Text("t", label, t)
					if it.Strike {
						w := rc.Shaper().Shape(label, t.Font).Advance
						rc.Box("strike", ui.Style{W: ui.Px(w), H: ui.Px(1), Fill: t.Color, Float: &ui.Float{At: render.Point{Y: .5}}}, nil)
					}
				})
				if it.Note != "" {
					rc.Text("note", it.Note, monoText(10.5, typeset.Normal, col(c, tLabel), 0))
				}
			})
			if o.Trailing != nil {
				rc.Row("trailing", ui.Style{Gap: 8, Cross: ui.Center}, func(rc *ui.Ctx) { o.Trailing(rc, *it, selected) })
			}
			if it.Mark != "" {
				rc.Text("mark", strings.ToUpper(it.Mark), monoText(10, typeset.SemiBold, it.MarkTone, 0))
			}
		})
	})
	if lr.Activated >= 0 && lr.Activated < len(flat) {
		it := flat[lr.Activated].item
		if len(it.Children) > 0 {
			if !hasKey(in.Keys, "CR") {
				toggle(lr.Activated)
			}
		} else if res.Activated == "" {
			res.Activated = it.ID
		}
	}
	if st.sel >= len(flat) {
		st.sel = max(len(flat)-1, 0)
	}
	if st.sel < len(flat) {
		res.Selected = flat[st.sel].item.ID
		st.selID = res.Selected
	}
	return res
}

// caretPainter draws a tree's caret as a glyph without making it text a reader would say.
func caretPainter(c *ui.Ctx, caret string, t ui.TextStyle) func(f render.Frame, r render.Rect) {
	run := c.Shaper().Shape(caret, t.Font)
	return func(f render.Frame, r render.Rect) {
		f.Glyphs(run, render.Point{X: r.Min.X, Y: r.Min.Y + (r.Dy()+run.Ascent-run.Descent)/2}, t.Color)
	}
}
