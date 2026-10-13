package widget

import (
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var small = render.Point{X: 600, Y: 400}

func TestButton(t *testing.T) {
	var id ui.ID
	var clicks, disabledClicks int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{Pad: ui.Pad(10), Gap: 10}, func(c *ui.Ctx) {
			id = c.Next("save")
			if Button(c, "save", "Save", ButtonOpts{Primary: true, Help: "test/button"}) {
				clicks++
			}
			if Button(c, "off", "Off", ButtonOpts{Disabled: true, Help: "test/button"}) {
				disabledClicks++
			}
		})
	})
	x, y := mid(h.rect(id))
	h.click(x, y)
	if clicks != 1 {
		t.Fatalf("a click clicked %d times", clicks)
	}
	h.u.Focus(id)
	h.frame()
	h.keys("CR")
	h.keys(" ")
	if clicks != 3 {
		t.Errorf("Enter and Space: %d clicks, want 3", clicks)
	}
	h.click(x+90, y)
	if disabledClicks != 0 {
		t.Error("a disabled button clicked")
	}
}

func TestInput(t *testing.T) {
	text := ""
	var id ui.ID
	var res InputResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			id = c.Next("q")
			res = Input(c, "q", &text, InputOpts{Placeholder: "Ask…", Help: "test/input"})
		})
	})
	r := h.rect(id)
	h.click(r.Min.X+5, r.Min.Y+10)
	if h.u.Focused() != id {
		t.Fatal("a click didn't focus the input")
	}
	h.keys("h", "e", "l", "l", "o", "BS")
	if text != "hell" {
		t.Fatalf("text %q after typing hello and backspace", text)
	}
	h.keys("Home", "y", "End", "!")
	if text != "yhell!" {
		t.Fatalf("text %q after Home y End !", text)
	}
	h.keys("+Left", "+Left", "X")
	if text != "yhelX" {
		t.Errorf("text %q: shift-select two and type should replace them", text)
	}
	h.keys("Left", "Left", "Del")
	if text != "yheX" {
		t.Errorf("text %q after Left Left Del from the end of yhelX", text)
	}
	h.keys("CR")
	if !res.Submitted {
		t.Error("Enter didn't submit")
	}
	// A click at the start puts the cursor there.
	h.click(r.Min.X+10, r.Min.Y+10)
	h.keys("[")
	if text != "[yheX" {
		t.Errorf("text %q: the click should have put the cursor first", text)
	}
	h.keys("Esc")
	if !res.Cancelled || h.u.Focused() == id {
		t.Error("Escape didn't cancel and drop focus")
	}
}

func TestList(t *testing.T) {
	sel := 0
	var id ui.ID
	var res ListResult
	var built []int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		built = built[:0]
		id = c.Next("list")
		res = List(c, "list", 1000, &sel, ListOpts{Height: ui.Px(220), Help: "test/list"}, func(c *ui.Ctx, i int, selected bool) {
			built = append(built, i)
			c.Text("t", "row", ui.TextStyle{})
		})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+3*22+5)
	if sel != 3 || res.Activated != 3 || h.u.Focused() != id {
		t.Fatalf("click on row 3: sel %d, activated %d, focused %v", sel, res.Activated, h.u.Focused() == id)
	}
	h.keys("j", "j", "Down", "k")
	if sel != 5 {
		t.Errorf("j j Down k from 3: %d", sel)
	}
	h.keys("G")
	h.frame()
	if sel != 999 || !slices.Contains(built, 999) {
		t.Errorf("G: sel %d; the last row should be built and in view", sel)
	}
	h.keys("g", "PageDown")
	if sel != 9 {
		t.Errorf("g then PageDown: %d, want a page of 9 (220px / 22 less one)", sel)
	}
	h.keys("CR")
	if res.Activated != 9 {
		t.Errorf("Enter activated %d", res.Activated)
	}
}

func tree() []TreeItem {
	return []TreeItem{
		{ID: "internal", Label: "internal", Children: []TreeItem{
			{ID: "pricing", Label: "pricing", Children: []TreeItem{{ID: "round.go", Label: "round.go", Mark: "M"}, {ID: "tiers.go", Label: "tiers.go"}}},
		}},
		{ID: "go.mod", Label: "go.mod"},
	}
}

func TestTree(t *testing.T) {
	var id ui.ID
	var res TreeResult
	items := tree()
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree"})
	})
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+11)
	if res.Toggled != "internal" && res.Selected != "internal" {
		t.Fatalf("click on a folder: %+v", res)
	}
	h.keys("j", "l", "l")
	if res.Selected != "round.go" {
		t.Errorf("j l l: selected %q, want round.go (open pricing, step in)", res.Selected)
	}
	h.keys("CR")
	if res.Activated != "round.go" {
		t.Errorf("Enter on a leaf: %+v", res)
	}
	h.keys("h")
	if res.Selected != "pricing" {
		t.Errorf("h on a leaf goes to its parent: %q", res.Selected)
	}
	h.keys("h", "j")
	if res.Selected != "go.mod" {
		t.Errorf("h closes pricing, then j: %q", res.Selected)
	}
}

func TestTabs(t *testing.T) {
	active := 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tabs")
		Tabs(c, "tabs", []string{"Agent", "Terminal", "Problems"}, &active, TabsOpts{Help: "test/tabs"})
	})
	second, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(second)
	h.click(x, y)
	if active != 1 {
		t.Fatalf("click on the second tab: %d", active)
	}
	h.keys("l", "l", "h")
	if active != 1 {
		t.Errorf("l l h from 1: %d, want 1 (l stops at the end)", active)
	}
}

var items = []MenuItem{
	{Label: "Open usage details"}, {Label: "Copy value", Shortcut: "y"}, {Separator: true},
	{Label: "Hide this tat", Disabled: true}, {Label: "Move to statusline…"},
}

func TestMenu(t *testing.T) {
	var id ui.ID
	chosen, closed := -1, false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("menu")
		ch, cl := Menu(c, "menu", items, MenuOpts{Help: "test/menu"})
		if ch >= 0 {
			chosen = ch
		}
		closed = closed || cl
	})
	h.u.Focus(id)
	h.frame()
	h.keys("j", "j", "CR")
	if chosen != 4 {
		t.Errorf("j j Enter chose %d; the separator and the disabled item are skipped", chosen)
	}
	row, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(row)
	h.click(x, y)
	if chosen != 1 {
		t.Errorf("click chose %d", chosen)
	}
	h.keys("Esc")
	if !closed {
		t.Error("Escape didn't close")
	}
}

func TestContextMenu(t *testing.T) {
	var target ui.ID
	var res ContextResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		target = c.Box("tat", ui.Style{W: ui.Px(80), H: ui.Px(20), Interactive: true, Help: "hud/usage#weekly-window"}, nil)
		res = ContextMenu(c, "ctx", target, items)
	})
	x, y := mid(h.rect(target))
	h.rightClick(x, y)
	h.frame()
	h.keys("k", "CR")
	if res.Help != "hud/usage#weekly-window" {
		t.Fatalf("k from the top wraps to the last item, Help: %+v", res)
	}
	h.rightClick(x, y)
	h.frame()
	h.keys("j", "CR")
	if res.Chosen != 1 {
		t.Errorf("j Enter: %+v", res)
	}
	h.rightClick(x, y)
	h.click(590, 390)
	h.keys0()
	if h.u.Focused() != 0 && h.u.KeyMode() == "menu" {
		t.Error("a click outside left the menu open")
	}
}

// keys0 runs a frame with no keys.
func (h *harness) keys0() { h.frame() }

func TestPopoverAndModal(t *testing.T) {
	var anchor, pop, modal, inner, cancel ui.ID
	pOpen, mOpen := false, false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		anchor = c.Box("anchor", ui.Style{W: ui.Px(40), H: ui.Px(20)}, nil)
		pop = c.Next("pop")
		Popover(c, "pop", anchor, &pOpen, PopoverOpts{Width: 200, Help: "test/popover"}, func(c *ui.Ctx) {
			c.Text(nil, "hello", ui.TextStyle{})
		})
		modal = c.Next("modal")
		Modal(c, "modal", &mOpen, "Settings", ModalOpts{Width: 300, Help: "test/modal"}, func(c *ui.Ctx) {
			inner = c.Next("ok")
			Button(c, "ok", "OK", ButtonOpts{Help: "test/button"})
			cancel = c.Next("cancel")
			Button(c, "cancel", "Cancel", ButtonOpts{Help: "test/button"})
		})
	})
	pOpen = true
	h.frame()
	h.frame()
	if r := h.rect(pop); r.Min.Y < 24 {
		t.Errorf("popover at %v, want below its anchor", r)
	}
	h.keys("Esc")
	if pOpen {
		t.Error("Escape didn't close the popover")
	}

	mOpen = true
	h.frame()
	h.frame()
	if h.u.Focused() != modal {
		t.Fatal("an opened modal doesn't have focus")
	}
	for range 5 {
		h.u.FocusNext(1)
		h.frame()
		if f := h.u.Focused(); f != modal && f != inner && f != cancel {
			t.Fatalf("focus left the modal: %v", f)
		}
	}
	h.u.Focus(modal)
	h.frame()
	h.keys("Esc")
	if mOpen {
		t.Error("Escape didn't close the modal")
	}
	mOpen = true
	h.frame()
	h.click(5, 395)
	if mOpen {
		t.Error("a click on the scrim didn't close the modal")
	}
}

func TestToasts(t *testing.T) {
	var ts Toasts
	var stack ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		stack = c.Next("toasts")
		ShowToasts(c, "toasts", &ts)
	})
	ts.Push(Toast{Title: "Saved", Tone: render.Color{G: 200, A: 255}, TTL: 3 * time.Second, Help: "test/toast"})
	ts.Push(Toast{Title: "Push failed", Body: "git push origin main", Tone: render.Color{R: 255, A: 255}, Help: "test/toast"})
	h.frame()
	if ts.Len() != 2 {
		t.Fatalf("%d toasts", ts.Len())
	}
	if want := h.now.Add(3 * time.Second); !h.u.NextFrame().Equal(want) {
		t.Errorf("next frame %v, want the timeout at %v", h.u.NextFrame(), want)
	}
	h.now = h.now.Add(3 * time.Second)
	h.frame()
	if ts.Len() != 1 {
		t.Errorf("after its TTL: %d toasts", ts.Len())
	}
	h.u.FocusNext(1)
	h.frame()
	h.keys("x")
	if ts.Len() != 0 {
		t.Error("x didn't dismiss the focused toast")
	}
	ts.Push(Toast{Title: "Again", Help: "test/toast"})
	h.frame()
	h.frame()
	x, y := mid(h.rect(ui.Child(stack, 3)))
	h.click(x, y)
	if ts.Len() != 0 {
		t.Error("a click didn't dismiss the toast")
	}
}

func TestSlider(t *testing.T) {
	v := 0.5
	var id ui.ID
	changed := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("hue")
		if Slider(c, "hue", &v, SliderOpts{Min: 0, Max: 360, Step: 1, Width: ui.Px(200), Label: "Hue",
			Track: func(t float64) render.Color { return theme.FromOKLCH(theme.OKLCH{L: .7, C: .15, H: t * 360, A: 1}) }}) {
			changed++
		}
	})
	if v != 0.5 {
		t.Fatalf("building changed the value to %v", v)
	}
	track := h.rect(id)
	_, y := mid(track)
	h.click(track.Min.X+track.Dx()/4, y)
	if v < 89 || v > 91 || changed == 0 {
		t.Fatalf("a click a quarter along: %v (changed %d)", v, changed)
	}
	h.pointer(render.PointerPress, render.ButtonPrimary, track.Min.X+track.Dx()/4, y)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, track.Max.X+50, y)
	h.frame()
	h.pointer(render.PointerRelease, render.ButtonPrimary, track.Max.X+50, y)
	h.frame()
	if v != 360 {
		t.Fatalf("dragged past the end: %v", v)
	}
	h.keys("h", "Left")
	if v != 358 {
		t.Errorf("two steps down: %v", v)
	}
	h.keys("+Right")
	if v != 360 {
		t.Errorf("shift-right is ten steps, clamped: %v", v)
	}
}

func TestClickable(t *testing.T) {
	var id ui.ID
	var in, off ui.Interaction
	clicks := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{Pad: ui.Pad(10), Gap: 10}, func(c *ui.Ctx) {
			id = c.Next("row")
			in = Clickable(c, "row", ui.Style{W: ui.Px(80), H: ui.Px(24), Focusable: true, Help: "test/clickable"}, nil)
			if in.Clicked {
				clicks++
			}
			off = Clickable(c, "off", ui.Style{W: ui.Px(80), H: ui.Px(24), Disabled: true, Help: "test/clickable"}, nil)
		})
	})
	x, y := mid(h.rect(id))
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if !in.Hovered || h.u.Cursor() != render.CursorPointer {
		t.Errorf("under the pointer: hovered %v, cursor %v", in.Hovered, h.u.Cursor())
	}
	h.click(x, y)
	if clicks != 1 {
		t.Fatalf("a click clicked %d times", clicks)
	}
	h.u.Focus(id)
	h.frame()
	h.keys("CR")
	if clicks != 2 {
		t.Errorf("Enter on the focused row: %d clicks, want 2", clicks)
	}
	h.click(x+90, y)
	if off.Clicked {
		t.Error("a disabled one clicked")
	}
	if n := h.access(id); n == nil || n.Role != render.RoleButton {
		t.Errorf("its role: %+v", n)
	}
}

func TestSegmented(t *testing.T) {
	active, changes := 0, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("seg")
		if Segmented(c, "seg", []string{"DARK", "LIGHT"}, &active, SegmentedOpts{Help: "test/segmented"}) {
			changes++
		}
	})
	second, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(second)
	h.click(x, y)
	h.click(x, y)
	if active != 1 || changes != 1 {
		t.Errorf("two clicks on the second: active %d, %d changes; want 1 and 1", active, changes)
	}
}

// An icon button in a row shows while the pointer's in the row, stays while it moves onto the button, and clicks.
func TestIconButtonInARow(t *testing.T) {
	var row ui.ID
	shown, clicks := false, 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		row = c.Next("row")
		Clickable(c, "row", ui.Style{Dir: ui.Row, W: ui.Px(200), H: ui.Px(30), Cross: ui.Center, Help: "test/row"}, func(c *ui.Ctx) {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			shown = PointerIn(c)
			if shown && IconButton(c, "x", "✕", IconButtonOpts{Label: "Forget", Help: "test/row"}) {
				clicks++
			}
		})
	})
	r := h.rect(row)
	h.pointer(render.PointerMove, 0, r.Min.X+20, r.Min.Y+15)
	h.frame()
	h.frame()
	if !shown {
		t.Fatal("the button isn't shown with the pointer in the row")
	}
	x, y := r.Max.X-10, r.Min.Y+15
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if !shown {
		t.Fatal("the button went when the pointer moved onto it")
	}
	h.click(x, y)
	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}
	h.pointer(render.PointerMove, 0, r.Max.X+50, y)
	h.frame()
	h.frame()
	if shown {
		t.Error("the button stays with the pointer gone")
	}
}

// texts are the runs of text the last frame drew, in order.
func (h *harness) texts() []string {
	var out []string
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs {
			out = append(out, op.Run.Text)
		}
	}
	return out
}

func (h *harness) drew(s string) bool {
	for _, t := range h.texts() {
		if t == s {
			return true
		}
	}
	return false
}

// A tree's rows can carry a note after the label, a tint, a strike-through and content at their right; items
// that say Open start expanded; and the tree takes keys in the mode its owner names.
func TestTreeRowsAndOptions(t *testing.T) {
	var id ui.ID
	var res TreeResult
	red := render.Color{R: 255, A: 255}
	items := []TreeItem{
		{ID: "staged", Label: "STAGED", Section: true, Open: true, Children: []TreeItem{
			{ID: "round.go", Label: "round.go", Note: "← amount.go", Tone: red, Strike: true},
		}},
		{ID: "closed", Label: "closed", Children: []TreeItem{{ID: "hidden.go", Label: "hidden.go"}}},
	}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree", KeyMode: "explorer",
			Trailing: func(c *ui.Ctx, it TreeItem, selected bool) {
				c.Text("n", "+31 -9 "+it.ID, ui.TextStyle{})
			}})
	})
	h.frame()
	for _, want := range []string{"STAGED", "round.go", "← amount.go", "+31 -9 round.go", "+31 -9 staged"} {
		if !h.drew(want) {
			t.Errorf("no %q in %q", want, h.texts())
		}
	}
	if h.drew("hidden.go") {
		t.Error("an item without Open started expanded")
	}
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+33)
	if res.Activated != "round.go" {
		t.Errorf("click on the open section's child: %+v", res)
	}
	if m := h.u.KeyMode(); m != "explorer" {
		t.Errorf("the focused tree takes keys in %q", m)
	}
	h.keys("k", "CR")
	if h.drew("round.go") {
		t.Error("Enter on an open section didn't close it")
	}
}

// A diff stat leaves out a side with nothing in it, and its bar splits between adds and deletes.
func TestDiffStat(t *testing.T) {
	var id ui.ID
	add, del := 31, 9
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("ds")
		DiffStat(c, "ds", add, del, DiffStatOpts{Bar: true, Scale: 40, Width: 40})
	})
	h.frame()
	if !h.drew("+31") || !h.drew("-9") {
		t.Errorf("drew %q", h.texts())
	}
	a, d := h.rect(ui.Child(ui.Child(id, "bar"), "a")), h.rect(ui.Child(ui.Child(id, "bar"), "d"))
	if a.Dx() != 31 || d.Dx() != 9 {
		t.Errorf("bar split %v / %v, want 31 / 9", a.Dx(), d.Dx())
	}
	add, del = 0, 62
	h.frame()
	if h.drew("+0") || !h.drew("-62") {
		t.Errorf("a delete-only change drew %q", h.texts())
	}
}

// A toggle chip flips on a click and on Space while focused.
func TestToggleChip(t *testing.T) {
	var id ui.ID
	on := false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("chip")
		on = ToggleChip(c, "chip", "± lines", on, ToggleChipOpts{Help: "test/chip"})
	})
	h.click(mid(h.rect(id)))
	if !on {
		t.Fatal("a click didn't turn it on")
	}
	h.frame()
	if n := h.access(id); n == nil || !n.Selected {
		t.Errorf("its accessible state: %+v", n)
	}
	h.keys(" ")
	if on {
		t.Error("Space didn't turn it off")
	}
}

// When the rows change under it (a file staged moves to another section), the selection stays on the same item.
func TestTreeSelectionFollowsItsItem(t *testing.T) {
	var res TreeResult
	items := []TreeItem{{ID: "a", Label: "a"}, {ID: "b", Label: "b"}, {ID: "c", Label: "c"}}
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree"})
	})
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+11+22)
	if res.Selected != "b" {
		t.Fatalf("selected %q", res.Selected)
	}
	items = []TreeItem{{ID: "b", Label: "b"}, {ID: "a", Label: "a"}, {ID: "c", Label: "c"}}
	h.frame()
	if res.Selected != "b" {
		t.Errorf("after b moved up, selected %q", res.Selected)
	}
	items = []TreeItem{{ID: "a", Label: "a"}, {ID: "c", Label: "c"}}
	h.frame()
	if res.Selected != "a" {
		t.Errorf("after b went, selected %q, want the row in its place", res.Selected)
	}
}
