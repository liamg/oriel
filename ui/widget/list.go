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

	role, rowRole ui.Role           // a tree is a list with tree roles
	expanded      func(i int) *bool // a tree row's expanded state
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
	// Keep the selection in view.
	if r, ok := c.Rect(id); ok && n > 0 {
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
		lc.Row("selection", ui.Style{W: ui.Px(r.Dx()), H: ui.Px(rh), Fill: col(c, tRowSelected), Z: -1, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: col(c, tRowEdge)}, nil)
		})
	}
	c.ListBehind(key, ui.Style{W: ui.Flex(1), H: h, Focusable: true, Help: o.Help, KeyMode: "list", Role: role, Label: o.Label}, n, rh, behind, func(rc *ui.Ctx, i int) {
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
	ID       string
	Label    string
	Swatch   render.Color // a small square before the label, as the explorer's file icons
	Mark     string       // right-aligned, as the explorer's M/A/D
	MarkTone render.Color
	Children []TreeItem
	Help     string
}

type TreeOpts struct {
	RowHeight float32 // 22 by default
	Height    ui.Size
	Help      string
	Label     string
}

type TreeResult struct {
	Selected  string // the selected item's ID
	Activated string // an item opened this frame, or ""
	Toggled   string // an item expanded or collapsed this frame, or ""
}

type treeState struct {
	open map[string]bool
	sel  int
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
			if st.open[items[i].ID] {
				walk(items[i].Children, depth+1, len(flat)-1)
			}
		}
	}
	walk(roots, 0, -1)
	var res TreeResult
	toggle := func(i int) {
		it := flat[i].item
		st.open[it.ID] = !st.open[it.ID]
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
				if !st.open[cur.item.ID] {
					toggle(st.sel)
				} else {
					st.sel++
				}
			}
		case hasKey([]render.KeyEvent{k}, "h", "Left"):
			if len(cur.item.Children) > 0 && st.open[cur.item.ID] {
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
		open := st.open[flat[i].item.ID]
		return &open
	}
	lo := ListOpts{RowHeight: rh, Height: o.Height, Help: o.Help, Label: o.Label, role: ui.RoleTree, rowRole: ui.RoleTreeItem, expanded: expanded}
	lr := List(c, key, len(flat), &st.sel, lo, func(rc *ui.Ctx, i int, selected bool) {
		it := flat[i].item
		t := label
		if selected {
			t.Color = col(c, tRowSelectedFg)
		}
		rc.Row("row", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 10 + float32(flat[i].depth)*14, R: 10}, Gap: 6, Cross: ui.Center, Help: it.Help}, func(rc *ui.Ctx) {
			if len(it.Children) > 0 {
				caret := "▸"
				if st.open[it.ID] {
					caret = "▾"
				}
				rc.Box("caret", ui.Style{Role: ui.RoleNone}, func(rc *ui.Ctx) { rc.Paint("glyph", ui.Px(8), ui.Px(14), caretPainter(c, caret, t)) })
			}
			if it.Swatch.A > 0 {
				rc.Box("swatch", ui.Style{W: ui.Px(10), H: ui.Px(10), Fill: alpha(it.Swatch, .85), Radius: 2}, nil)
			}
			rc.Text("label", it.Label, t)
			if it.Mark != "" {
				rc.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
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
	if st.sel < len(flat) {
		res.Selected = flat[st.sel].item.ID
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
