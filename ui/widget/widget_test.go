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
